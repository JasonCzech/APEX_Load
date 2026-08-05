package api

import (
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// terminalRuntimeCompatibilityStore deliberately exposes only legacy full-row
// test persistence. It models embedders that have not implemented the targeted
// ref-key, baseline, or provenance update extensions.
type terminalRuntimeCompatibilityStore struct {
	*metricsStoreSpy
	record *storage.TestRecord
}

func (s *terminalRuntimeCompatibilityStore) SaveTest(record *storage.TestRecord) error {
	if record == nil {
		s.record = nil
		return nil
	}
	cloned := *record
	s.record = &cloned
	return nil
}

func (s *terminalRuntimeCompatibilityStore) GetTest(id string) (*storage.TestRecord, error) {
	if s.record == nil || s.record.ID != id {
		return nil, nil
	}
	cloned := *s.record
	return &cloned, nil
}

func assertStoredTerminalRuntimePreserved(t *testing.T, record *storage.TestRecord) {
	t.Helper()
	if record == nil {
		t.Fatal("durable test record is nil")
	}
	groups := decodeStoredGroups(record.Groups)
	if len(groups) != 1 {
		t.Fatalf("durable groups = %#v, want one group", groups)
	}
	group := groups[0]
	if group.script == nil || group.script.ID != "runtime-script" {
		t.Fatalf("durable runtime script = %#v, want preserved snapshot", group.script)
	}
	if group.baseScript == nil || group.baseScript.ID != "base-script" {
		t.Fatalf("durable base script = %#v, want preserved snapshot", group.baseScript)
	}
	if len(group.sharedDataSources) != 1 || group.sharedDataSources[0].Name != "accounts" {
		t.Fatalf("durable shared data sources = %#v, want preserved projection", group.sharedDataSources)
	}
	if len(group.redactionRules) != 1 || group.redactionRules[0].Pattern != `secret-[0-9]+` {
		t.Fatalf("durable redaction rules = %#v, want preserved policy", group.redactionRules)
	}
}

func TestTerminalCompactionPreservesDurableRuntimeAcrossCompatibilitySaves(t *testing.T) {
	createdAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	endedAt := createdAt.Add(time.Minute)
	seed := &ManagedTest{
		ID:        "terminal-compatibility-save",
		Name:      "terminal compatibility save",
		Status:    "COMPLETE",
		ProjectID: storage.DefaultProjectID,
		CreatedAt: createdAt,
		EndedAt:   &endedAt,
		Groups: []TestGroup{{
			Name:              "group",
			ScriptID:          "runtime-script",
			script:            &dsl.Script{ID: "runtime-script", Name: "runtime"},
			baseScript:        &dsl.Script{ID: "base-script", Name: "base"},
			sharedDataSources: []dsl.DataSource{{Name: "accounts", Type: "csv", InlineData: "id\n1", Strategy: "sequential"}},
			redactionRules:    []engine.RedactionRule{{Name: "secret", Pattern: `secret-[0-9]+`}},
		}},
	}
	record := buildManagedTestRecord(seed)
	store := &terminalRuntimeCompatibilityStore{
		metricsStoreSpy: &metricsStoreSpy{listedTests: []*storage.TestRecord{record}},
		record:          record,
	}
	runner := NewTestRunner(2, NewHub(), store)

	canonical := runner.tests[seed.ID]
	if canonical == nil || !canonical.terminalGroupRuntimeTrimmed {
		t.Fatalf("terminal canonical state was not compacted: %#v", canonical)
	}
	group := canonical.Groups[0]
	if group.script != nil || group.baseScript != nil || group.sharedDataSources != nil || group.redactionRules != nil || group.runtimeSecretValues != nil {
		t.Fatalf("terminal runtime remained resident after compaction: %#v", group)
	}
	if canonical.RefKey == "" || store.record.RefKey == "" {
		t.Fatalf("compatibility ref-key backfill was not persisted: canonical=%q durable=%q", canonical.RefKey, store.record.RefKey)
	}
	assertStoredTerminalRuntimePreserved(t, store.record)

	// A later compatibility-only baseline mutation also uses full-row SaveTest.
	// It must not turn the compacted in-memory projection into durable data loss.
	runner.mu.Lock()
	canonical.BaselinePinned = true
	pinnedAt := endedAt.Add(time.Second)
	canonical.BaselinePinnedAt = &pinnedAt
	if err := runner.persistBaselineChanges(map[string]*time.Time{canonical.ID: &pinnedAt}); err != nil {
		runner.mu.Unlock()
		t.Fatal(err)
	}
	runner.mu.Unlock()
	assertStoredTerminalRuntimePreserved(t, store.record)
	if !store.record.BaselinePinned || store.record.BaselinePinnedAt == nil || !store.record.BaselinePinnedAt.Equal(pinnedAt) {
		t.Fatalf("compatibility baseline mutation was not persisted: %#v", store.record)
	}
}

func TestCompatibilitySaveFencesMissingOrReplacedTerminalRuntimeSnapshot(t *testing.T) {
	createdAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	test := &ManagedTest{
		ID:                          "terminal-identity-fence",
		Status:                      "COMPLETE",
		ProjectID:                   "project-a",
		CreatedAt:                   createdAt,
		terminalGroupRuntimeTrimmed: true,
	}
	tests := []struct {
		name    string
		record  *storage.TestRecord
		wantErr error
	}{
		{name: "missing", wantErr: errStoredTestMissing},
		{
			name:    "replaced project identity",
			record:  &storage.TestRecord{ID: test.ID, ProjectID: "project-b", StartTime: createdAt},
			wantErr: errStoredTestTransitionFenced,
		},
		{
			name:    "replaced run identity",
			record:  &storage.TestRecord{ID: test.ID, ProjectID: test.ProjectID, StartTime: createdAt.Add(time.Second)},
			wantErr: errStoredTestTransitionFenced,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &terminalRuntimeCompatibilityStore{metricsStoreSpy: &metricsStoreSpy{}, record: tc.record}
			runner := &TestRunner{testStore: store}
			err := runner.saveManagedTestRecord(test)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("save error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

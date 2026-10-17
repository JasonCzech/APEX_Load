package api

import (
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type blockedContentStore struct {
	*storage.SQLiteStore
	method           string
	entered, release chan struct{}
	once             sync.Once
}

func (s *blockedContentStore) block(method string) {
	if s.method == method {
		s.once.Do(func() { close(s.entered); <-s.release })
	}
}
func (s *blockedContentStore) GetScript(id string) (*dsl.Script, error) {
	script, err := s.SQLiteStore.GetScript(id)
	s.block("read") // keep the snapshot taken before a concurrent publication
	return script, err
}
func (s *blockedContentStore) GetScriptAuthoringSnapshot(id string) (*storage.ScriptAuthoringSnapshot, error) {
	s.block("authoring-read")
	return s.SQLiteStore.GetScriptAuthoringSnapshot(id)
}
func (s *blockedContentStore) CreateScriptAuthoringIfAbsent(script *dsl.Script) (*storage.ScriptAuthoringMutationResult, error) {
	s.block("create")
	return s.SQLiteStore.CreateScriptAuthoringIfAbsent(script)
}
func (s *blockedContentStore) UpdateScriptAuthoringIfCurrent(script *dsl.Script, expected storage.ScriptAuthoringPrecondition) (*storage.ScriptAuthoringMutationResult, error) {
	s.block("update")
	return s.SQLiteStore.UpdateScriptAuthoringIfCurrent(script, expected)
}
func (s *blockedContentStore) UpdateScriptIfUnchanged(script *dsl.Script, expected time.Time) (bool, error) {
	s.block("conditional-update")
	return s.SQLiteStore.UpdateScriptIfUnchanged(script, expected)
}
func (s *blockedContentStore) DeleteScriptIfUnchanged(id string, expected time.Time) (bool, error) {
	s.block("delete")
	return s.SQLiteStore.DeleteScriptIfUnchanged(id, expected)
}
func (s *blockedContentStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	s.block("import-create")
	return s.SQLiteStore.CreateScriptIfAbsent(script)
}
func (s *blockedContentStore) CommitGitHubScriptImport(script *dsl.Script, link *storage.GitHubScriptLink, expected time.Time) (storage.GitHubScriptImportOutcome, error) {
	s.block("github")
	return s.SQLiteStore.CommitGitHubScriptImport(script, link, expected)
}

func newContentMutationFixture(t *testing.T) (*TestRunner, *blockedContentStore, *dsl.Script) {
	t.Helper()
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "content.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	seedAuthoringScriptProjects(t, base, "content-project")
	script := &dsl.Script{ID: "content-script", ProjectID: "content-project", Name: "Original", Version: "1", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}}
	if err := base.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	store := &blockedContentStore{SQLiteStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	runner := NewTestRunner(1, NewHub(), store)
	script, err = base.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { releaseContentStore(store) })
	// Tests call releaseContentStore, whose channel close is idempotent.
	return runner, store, script
}
func releaseContentStore(store *blockedContentStore) {
	select {
	case <-store.release:
	default:
		close(store.release)
	}
}

func assertContentIOKeepsRuntimeAvailable(t *testing.T, runner *TestRunner) {
	t.Helper()
	if !runner.mu.TryRLock() {
		t.Fatal("content I/O held the live runner mutex")
	}
	runner.mu.RUnlock()
	if got := runner.durableWritesInFlight.Load(); got != 0 {
		t.Fatalf("off-lock content I/O entered lock-held gauge: %d", got)
	}
	stopped := make(chan struct{})
	go func() { _ = runner.StopTest("absent-live-run"); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("StopTest waited for content I/O")
	}
	published := make(chan struct{})
	go func() {
		runner.publishLiveMetrics(&ManagedTest{ID: "metrics-run", ProjectID: "content-project"}, &LiveMetrics{Timestamp: time.Now()}, nil, func() bool { return false })
		close(published)
	}()
	select {
	case <-published:
	case <-time.After(2 * time.Second):
		t.Fatal("metrics publication waited for content I/O")
	}
}

func TestContentScriptMutationsReleaseLiveRunnerDuringRepositoryIO(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		mutate       func(*TestRunner, *dsl.Script) error
	}{
		{"create", "create", func(r *TestRunner, s *dsl.Script) error {
			s.ID = "new-content"
			_, _, err := r.createScriptDurable(s, false, false, "", true)
			return err
		}},
		{"import", "import-create", func(r *TestRunner, s *dsl.Script) error {
			s.ID = "new-content"
			_, _, err := r.CreateImportedScriptDurableReportingReplay(s, "har")
			return err
		}},
		{"update-read", "authoring-read", func(r *TestRunner, s *dsl.Script) error {
			s.Name = "Updated"
			_, _, err := r.UpdateScriptDurable(s.ID, s)
			return err
		}},
		{"update", "update", func(r *TestRunner, s *dsl.Script) error {
			s.Name = "Updated"
			_, _, err := r.UpdateScriptDurable(s.ID, s)
			return err
		}},
		{"conditional-update", "conditional-update", func(r *TestRunner, s *dsl.Script) error {
			candidate := cloneScriptDefinition(s)
			candidate.Name = "Updated"
			_, _, err := r.UpdateScriptDurableIfUnchanged(candidate, s)
			return err
		}},
		{"move", "conditional-update", func(r *TestRunner, s *dsl.Script) error { _, err := r.MoveScriptDurable(s.ID, nil); return err }},
		{"delete", "delete", func(r *TestRunner, s *dsl.Script) error { _, err := r.DeleteScriptDurable(s.ID); return err }},
		{"conditional-delete", "delete", func(r *TestRunner, s *dsl.Script) error {
			_, err := r.DeleteScriptDurableIfUnchanged(s.ID, s.UpdatedAt)
			return err
		}},
		{"github", "github", func(r *TestRunner, s *dsl.Script) error {
			s.ID = "github-new"
			_, _, err := r.StoreGitHubScriptImportDurable(s, &storage.GitHubScriptLink{Owner: "example", Repo: "repo", Branch: "main", Path: "script.json"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, store, script := newContentMutationFixture(t)
			store.method = tc.method
			done := make(chan error, 1)
			go func() { done <- tc.mutate(runner, script) }()
			select {
			case <-store.entered:
			case err := <-done:
				t.Fatalf("mutation returned before %s barrier: %v", tc.method, err)
			case <-time.After(5 * time.Second):
				t.Fatal("mutation did not reach repository")
			}
			assertContentIOKeepsRuntimeAvailable(t, runner)
			releaseContentStore(store)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("mutation did not finish after repository resumed")
			}
		})
	}
}

func TestContentDurableReadCannotOverwriteNewerMutation(t *testing.T) {
	runner, store, script := newContentMutationFixture(t)
	store.method = "read"
	done := make(chan error, 1)
	go func() { _, _, err := runner.GetScriptDurable(script.ID); done <- err }()
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("read did not reach barrier")
	}
	assertContentIOKeepsRuntimeAvailable(t, runner)
	candidate := cloneScriptDefinition(script)
	candidate.Name = "Newer committed edit"
	if _, ok, err := runner.UpdateScriptDurable(script.ID, candidate); err != nil || !ok {
		t.Fatalf("update: %v %v", ok, err)
	}
	releaseContentStore(store)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	runner.mu.RLock()
	cached := cloneStoredScriptForReadLocked(runner.scripts[script.ID])
	runner.mu.RUnlock()
	if cached.Name != candidate.Name {
		t.Fatalf("stale read replaced committed edit: %q", cached.Name)
	}
}

func TestContentFailedMutationDoesNotPublishSpeculativeCache(t *testing.T) {
	runner, store, script := newContentMutationFixture(t)
	// A different durable revision wins while this replica retains the old one.
	newer := cloneScriptDefinition(script)
	newer.Name = "Remote editor"
	if err := store.SQLiteStore.SaveScript(newer); err != nil {
		t.Fatal(err)
	}
	candidate := cloneScriptDefinition(script)
	candidate.Name = "Rejected candidate"
	if _, applied, err := runner.UpdateScriptDurableIfUnchanged(candidate, script); applied || (err != nil && !errors.Is(err, errScriptConcurrentChange)) {
		t.Fatalf("stale update: applied=%v err=%v", applied, err)
	}
	runner.mu.RLock()
	cached := cloneStoredScriptForReadLocked(runner.scripts[script.ID])
	runner.mu.RUnlock()
	if cached.Name == candidate.Name {
		t.Fatal("rejected update became visible in the cache")
	}
}

func (s *blockedContentStore) DeleteScenario(id string) error {
	s.block("scenario-delete")
	return s.SQLiteStore.DeleteScenario(id)
}
func (s *blockedContentStore) DeleteScenarioIfUnchanged(id string, expected time.Time) (bool, error) {
	s.block("scenario-conditional-delete")
	return s.SQLiteStore.DeleteScenarioIfUnchanged(id, expected)
}
func (s *blockedContentStore) UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding, expected time.Time) (bool, error) {
	s.block("scenario-update")
	return s.SQLiteStore.UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario, scripts, bindings, expected)
}
func (s *blockedContentStore) GetScenario(id string) (*storage.Scenario, []storage.ScenarioScript, error) {
	scenario, groups, err := s.SQLiteStore.GetScenario(id)
	s.block("scenario-read")
	return scenario, groups, err
}

func newContentScenario(t *testing.T, runner *TestRunner, script *dsl.Script) *StoredScenario {
	t.Helper()
	scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Scenario", Duration: "30s", Groups: []CreateGroupEntry{
		{Name: "Source", ScriptID: script.ID, VUsers: 1, RuntimeSettings: &RuntimeSettingsOverride{Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 500}}},
		{Name: "Target", ScriptID: script.ID, VUsers: 1},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return scenario
}

func TestContentScenarioMutationsReleaseLiveRunnerDuringRepositoryIO(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		mutate       func(*TestRunner, *StoredScenario) error
	}{
		{"delete", "scenario-delete", func(r *TestRunner, s *StoredScenario) error { _, err := r.DeleteScenarioDurable(s.ID); return err }},
		{"conditional-delete", "scenario-conditional-delete", func(r *TestRunner, s *StoredScenario) error {
			_, err := r.DeleteScenarioIfUnchanged(s.ID, s.UpdatedAt)
			return err
		}},
		{"runtime-apply", "scenario-update", func(r *TestRunner, s *StoredScenario) error {
			_, _, err := r.ApplyScenarioRuntimeToGroups(s.ID, ApplyRuntimeToGroupsRequest{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"pacing"}})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, store, script := newContentMutationFixture(t)
			scenario := newContentScenario(t, runner, script)
			store.method = tc.method
			done := make(chan error, 1)
			go func() { done <- tc.mutate(runner, scenario) }()
			select {
			case <-store.entered:
			case err := <-done:
				t.Fatalf("mutation returned before barrier: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("mutation did not reach repository")
			}
			assertContentIOKeepsRuntimeAvailable(t, runner)
			releaseContentStore(store)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("mutation did not complete")
			}
		})
	}
}

func TestContentScenarioRefreshCannotOverwriteRuntimeEdit(t *testing.T) {
	runner, store, script := newContentMutationFixture(t)
	scenario := newContentScenario(t, runner, script)
	store.method = "scenario-read"
	done := make(chan error, 1)
	go func() { _, err := runner.refreshArchivedScenario(scenario.ID); done <- err }()
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scenario read did not reach barrier")
	}
	assertContentIOKeepsRuntimeAvailable(t, runner)
	if _, ok, err := runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"pacing"}}); err != nil || !ok {
		t.Fatalf("runtime apply: ok=%v err=%v", ok, err)
	}
	releaseContentStore(store)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	runner.mu.RLock()
	cached := cloneStoredScenarioForReadLocked(runner.scenarios[scenario.ID])
	runner.mu.RUnlock()
	if cached.Scripts[1].RuntimeSettings == nil || cached.Scripts[1].RuntimeSettings.Pacing == nil || cached.Scripts[1].RuntimeSettings.Pacing.DelayMs != 500 {
		t.Fatal("stale scenario refresh replaced runtime edit")
	}
}

func TestContentRuntimeApplyPreservesSchedulePublishedDuringStorageCall(t *testing.T) {
	runner, store, script := newContentMutationFixture(t)
	scenario := newContentScenario(t, runner, script)
	store.method = "scenario-update"
	done := make(chan error, 1)
	go func() {
		_, _, err := runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"pacing"}})
		done <- err
	}()
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime apply did not reach barrier")
	}
	// Scheduler publication owns a separate durable row and may finish while a
	// content transaction is in flight. The content result must not discard it.
	runner.mu.Lock()
	runner.scenarios[scenario.ID].Schedules = []*storage.ScenarioSchedule{{ID: "new-schedule", ScenarioID: scenario.ID, Revision: 2}}
	runner.contentRevision++
	runner.mu.Unlock()
	releaseContentStore(store)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	runner.mu.RLock()
	cached := cloneStoredScenarioForReadLocked(runner.scenarios[scenario.ID])
	runner.mu.RUnlock()
	if len(cached.Schedules) != 1 || cached.Schedules[0].ID != "new-schedule" || cached.Schedules[0].Revision != 2 {
		t.Fatalf("schedule lost: %#v", cached.Schedules)
	}
}

// Runtime compilation reads project defaults through the private snapshot. This
// barrier observes the real validation path without adding production hooks.
type scenarioValidationBarrierStore struct {
	*storage.SQLiteStore
	entered, release chan struct{}
	blocked          atomic.Bool
}

func (s *scenarioValidationBarrierStore) GetProject(id string) (*storage.Project, error) {
	if s.blocked.CompareAndSwap(false, true) {
		close(s.entered)
		<-s.release
	}
	return s.SQLiteStore.GetProject(id)
}

func awaitContentMutationResult(t *testing.T, result <-chan error, operation string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not complete", operation)
		return nil
	}
}

func TestContentRuntimeApplyRejectsSourceEditDuringValidation(t *testing.T) {
	for _, alreadyInFlight := range []bool{false, true} {
		t.Run(map[bool]string{false: "edit completes during validation", true: "edit already in flight"}[alreadyInFlight], func(t *testing.T) {
			runner, store, script := newContentMutationFixture(t)
			scenario := newContentScenario(t, runner, script)
			before, groupsBefore, err := store.SQLiteStore.GetScenario(scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			barrier := &scenarioValidationBarrierStore{SQLiteStore: store.SQLiteStore, entered: make(chan struct{}), release: make(chan struct{})}
			var releaseValidation sync.Once
			release := func() { releaseValidation.Do(func() { close(barrier.release) }) }
			var workers sync.WaitGroup
			t.Cleanup(func() {
				release()
				releaseContentStore(store)
				done := make(chan struct{})
				go func() { workers.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("content mutation workers did not settle during cleanup")
				}
			})
			runner.projectStore = barrier
			edit := cloneScriptDefinition(script)
			edit.Name = "Changed source"
			edit.Config.BaseURL = "https://changed.example.test"
			editDone := make(chan error, 1)
			startEdit := func() {
				workers.Add(1)
				go func() {
					defer workers.Done()
					_, _, err := runner.UpdateScriptDurable(script.ID, edit)
					editDone <- err
				}()
			}
			if alreadyInFlight {
				store.method = "update"
				startEdit()
				select {
				case <-store.entered:
				case err := <-editDone:
					t.Fatalf("edit missed repository barrier: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("edit did not enter repository")
				}
			}
			applied := make(chan error, 1)
			workers.Add(1)
			go func() {
				defer workers.Done()
				_, _, err := runner.ApplyScenarioRuntimeToGroups(scenario.ID, ApplyRuntimeToGroupsRequest{SourceGroup: 0, TargetGroups: []int{1}, Fields: []string{"pacing", "base_url"}})
				applied <- err
			}()
			select {
			case <-barrier.entered:
			case err := <-applied:
				t.Fatalf("apply missed validation barrier: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("apply did not enter validation")
			}
			assertContentIOKeepsRuntimeAvailable(t, runner)
			if !alreadyInFlight {
				startEdit()
				if err := awaitContentMutationResult(t, editDone, "source edit"); err != nil {
					t.Fatal(err)
				}
			}
			release()
			if err := awaitContentMutationResult(t, applied, "runtime apply"); !errors.Is(err, errScenarioConcurrentChange) {
				t.Fatalf("runtime apply accepted stale source: %v", err)
			}
			if alreadyInFlight {
				releaseContentStore(store)
				if err := awaitContentMutationResult(t, editDone, "source edit"); err != nil {
					t.Fatal(err)
				}
			}
			after, groupsAfter, err := store.SQLiteStore.GetScenario(scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(groupsBefore, groupsAfter) {
				t.Fatalf("stale runtime apply changed durable scenario: before=%+v after=%+v", before, after)
			}
			runner.mu.RLock()
			cached := cloneStoredScenarioForReadLocked(runner.scenarios[scenario.ID])
			runner.mu.RUnlock()
			if !reflect.DeepEqual(cached.RuntimeSettings, scenario.RuntimeSettings) || !reflect.DeepEqual(cached.Scripts, scenario.Scripts) || !cached.UpdatedAt.Equal(scenario.UpdatedAt) {
				t.Fatal("stale runtime apply published speculative cached state")
			}
		})
	}
}

package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestQueuedRunRevisionUsesImmutableRuntimeScriptAndDataSnapshot(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	now := time.Now().UTC()
	pool := &storage.SharedDataPool{
		ID: "revision-pool", Name: "Revision pool", ProjectID: storage.DefaultProjectID,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatalf("SaveDataPool: %v", err)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "revision-row", PoolID: pool.ID, Data: map[string]string{"version": "before"}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveDataPoolRow(before): %v", err)
	}

	script := httpScript("revision-source-script", "Revision source")
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenarioRequest := CreateScenarioRequest{
		Name: "Mutable revision scenario", Duration: "1m",
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"ROOT_TOKEN": "scenario-a"}},
		Groups: []CreateGroupEntry{{
			Name: "Primary", ScriptID: script.ID, VUsers: 1,
			RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"GROUP_TOKEN": "scenario-group-a"}},
		}},
		DataPools: []storage.ScenarioDataPoolBinding{{
			PoolID: pool.ID, PoolName: pool.Name, Alias: "revision_data", AccessMode: "read_only", SelectionStrategy: "sequential",
		}},
	}
	scenario, err := server.runner.StoreScenario(scenarioRequest)
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}

	original, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Queued source", ScenarioID: scenario.ID,
		RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"ROOT_TOKEN": "queued-b"}},
		Groups: []CreateGroupEntry{{
			Name: "Primary", ScriptID: script.ID, VUsers: 1,
			RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"GROUP_TOKEN": "queued-group-b"}},
		}},
		creationActor: testCreationActor{Type: "user", Subject: "revision-author"},
	})
	if err != nil {
		t.Fatalf("CreateTest(source): %v", err)
	}
	if original.Status != "QUEUED" || len(original.groupSpecs) != 1 || original.Groups[0].baseScript == nil || len(original.Groups[0].sharedDataSources) != 1 {
		t.Fatalf("source snapshot incomplete: status=%s groups=%+v", original.Status, original.Groups)
	}
	originalActionTarget := original.groupSpecs[0].Script.Actions[0].Target
	originalInlineData := original.groupSpecs[0].Script.DataSources[len(original.groupSpecs[0].Script.DataSources)-1].InlineData
	if !strings.Contains(originalInlineData, "before") {
		t.Fatalf("source data snapshot = %q, want before", originalInlineData)
	}

	// Mutate every live resource a naive revision would accidentally re-read.
	server.runner.mu.Lock()
	server.runner.scenarios[scenario.ID].RuntimeSettings = &RuntimeSettingsOverride{Env: map[string]string{"ROOT_TOKEN": "scenario-c"}}
	server.runner.scenarios[scenario.ID].Scripts[0].RuntimeSettings = &RuntimeSettingsOverride{Env: map[string]string{"GROUP_TOKEN": "scenario-group-c"}}
	server.runner.mu.Unlock()
	mutatedScript := cloneScriptForStoredGroup(script)
	mutatedScript.Actions[0].Target = "/mutated-live-script"
	if _, found, updateErr := server.runner.UpdateScriptDurable(script.ID, mutatedScript); updateErr != nil || !found {
		t.Fatalf("UpdateScriptDurable = found %t, err %v", found, updateErr)
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{
		ID: "revision-row", PoolID: pool.ID, Data: map[string]string{"version": "after"}, CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveDataPoolRow(after): %v", err)
	}

	masked := maskManagedTest(original)
	sourceIndex := 0
	revisionRequest := CreateTestRequest{
		Name: "Revised queued copy", RevisionSourceTestID: original.ID,
		RuntimeSettings: masked.RuntimeSettings,
		DataPools:       cloneScenarioDataPoolBindings(masked.DataPools),
		Groups: []CreateGroupEntry{{
			Name: "Renamed primary", ScriptID: masked.Groups[0].ScriptID, VUsers: masked.Groups[0].VUsers,
			RevisionSourceGroupIndex: &sourceIndex,
			RuntimeSettings:          masked.Groups[0].RuntimeSettings,
			InitStrategy:             masked.Groups[0].Initialization,
		}},
		creationActor: testCreationActor{Type: "user", Subject: "revision-author"},
	}
	// Exercise the real JSON boundary: timestamps lose monotonic/location
	// internals and masked values arrive exactly as the dashboard sends them.
	payload, err := json.Marshal(revisionRequest)
	if err != nil {
		t.Fatalf("marshal revision request: %v", err)
	}
	var roundTripped CreateTestRequest
	if err := json.Unmarshal(payload, &roundTripped); err != nil {
		t.Fatalf("unmarshal revision request: %v", err)
	}
	roundTripped.creationActor = revisionRequest.creationActor
	revision, err := server.runner.CreateTest(roundTripped)
	if err != nil {
		t.Fatalf("CreateTest(revision): %v", err)
	}

	if got := revision.RuntimeSettings.Env["ROOT_TOKEN"]; got != original.RuntimeSettings.Env["ROOT_TOKEN"] {
		t.Fatalf("root credential ref = %q, want queued source %q", got, original.RuntimeSettings.Env["ROOT_TOKEN"])
	}
	if got := revision.Groups[0].RuntimeSettings.Env["GROUP_TOKEN"]; got != original.Groups[0].RuntimeSettings.Env["GROUP_TOKEN"] {
		t.Fatalf("group credential ref = %q, want queued source %q", got, original.Groups[0].RuntimeSettings.Env["GROUP_TOKEN"])
	}
	if got := revision.groupSpecs[0].Script.Actions[0].Target; got != originalActionTarget {
		t.Fatalf("revision action target = %q, want immutable %q", got, originalActionTarget)
	}
	inlineData := revision.groupSpecs[0].Script.DataSources[len(revision.groupSpecs[0].Script.DataSources)-1].InlineData
	if inlineData != originalInlineData || strings.Contains(inlineData, "after") {
		t.Fatalf("revision data snapshot = %q, want immutable %q", inlineData, originalInlineData)
	}
}

func TestQueuedRunRevisionSourceBoundScriptIgnoresLiveLibraryState(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	now := time.Now().UTC()
	for _, project := range []*storage.Project{
		{ID: "project-a", Name: "Project A", CreatedAt: now, UpdatedAt: now},
		{ID: "project-b", Name: "Project B", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveProject(project); err != nil {
			t.Fatalf("SaveProject(%s): %v", project.ID, err)
		}
	}

	sourceScript := httpScript("revision-source-bound-script", "Source-bound script")
	sourceScript.ProjectID = "project-a"
	sourceScript.Actions[0].Target = "/immutable-source-action"
	if _, err := server.runner.StoreScriptDurable(sourceScript); err != nil {
		t.Fatalf("StoreScriptDurable(source): %v", err)
	}

	source, err := server.runner.CreateTest(CreateTestRequest{
		Name:      "Queued source-bound run",
		ProjectID: "project-a",
		Duration:  "1m",
		Groups: []CreateGroupEntry{{
			Name: "Primary", ScriptID: sourceScript.ID, VUsers: 1,
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest(source): %v", err)
	}
	if source.Status != "QUEUED" || len(source.Groups) != 1 || source.Groups[0].script == nil {
		t.Fatalf("source is not a complete queued snapshot: status=%s groups=%+v", source.Status, source.Groups)
	}
	if source.ApplicationID != storage.LegacyApplicationID || source.ReleaseID != storage.LegacyReleaseID {
		t.Fatalf("source fallback context = application %q release %q, want public legacy IDs", source.ApplicationID, source.ReleaseID)
	}

	if deleted, deleteErr := server.runner.DeleteScriptDurableInProject(sourceScript.ID, "project-a"); deleteErr != nil || !deleted {
		t.Fatalf("DeleteScriptDurableInProject(source) = %t, %v", deleted, deleteErr)
	}

	revise := func(name string) *ManagedTest {
		t.Helper()
		revision, createErr := server.runner.CreateTest(CreateTestRequest{
			Name:                 name,
			RevisionSourceTestID: source.ID,
			ApplicationID:        source.ApplicationID,
			ReleaseID:            source.ReleaseID,
			Groups:               createGroupEntriesFromManagedTest(source),
		})
		if createErr != nil {
			t.Fatalf("CreateTest(%s): %v", name, createErr)
		}
		if got := revision.groupSpecs[0].Script.Actions[0].Target; got != "/immutable-source-action" {
			t.Fatalf("%s action target = %q, want immutable source action", name, got)
		}
		if got := normalizeProjectID(revision.ProjectID); got != "project-a" {
			t.Fatalf("%s project = %q, want project-a", name, got)
		}
		return revision
	}

	// Deleting the library row must not invalidate the queued run's durable
	// runtime snapshot.
	revise("Revision after source deletion")

	// Reusing the same library ID in another project must not poison project
	// resolution or replace the source-bound runtime snapshot.
	foreignReplacement := httpScript(sourceScript.ID, "Foreign replacement")
	foreignReplacement.ProjectID = "project-b"
	foreignReplacement.Actions[0].Target = "/foreign-live-action"
	if _, err := server.runner.StoreScriptDurable(foreignReplacement); err != nil {
		t.Fatalf("StoreScriptDurable(foreign replacement): %v", err)
	}
	revise("Revision after foreign same-ID replacement")

	// An explicitly new group has no source binding and must still resolve its
	// current library script. A deleted row therefore remains a hard failure.
	newGroupScript := httpScript("deleted-revision-new-group", "Deleted new-group script")
	newGroupScript.ProjectID = "project-a"
	if _, err := server.runner.StoreScriptDurable(newGroupScript); err != nil {
		t.Fatalf("StoreScriptDurable(new group): %v", err)
	}
	if deleted, deleteErr := server.runner.DeleteScriptDurableInProject(newGroupScript.ID, "project-a"); deleteErr != nil || !deleted {
		t.Fatalf("DeleteScriptDurableInProject(new group) = %t, %v", deleted, deleteErr)
	}
	_, err = server.runner.CreateTest(CreateTestRequest{
		Name:                 "Revision with deleted new group",
		RevisionSourceTestID: source.ID,
		Groups: []CreateGroupEntry{{
			Name: "Explicit new group", ScriptID: newGroupScript.ID, RevisionNewGroup: true, VUsers: 1,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "script not found") {
		t.Fatalf("deleted revision_new_group error = %v, want script not found", err)
	}
}

func TestQueuedRunRevisionRefKeyInheritsReleaseEvidence(t *testing.T) {
	server, store := newRuntimeOverrideTestServer(t)
	now := time.Now().UTC()
	if err := store.SaveApplication(&storage.Application{
		ID: "revision-ref-app", ProjectID: storage.DefaultProjectID, Name: "Revision Ref", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	if err := store.SaveRelease(&storage.Release{
		ID: "revision-ref-release", ApplicationID: "revision-ref-app", ProjectID: storage.DefaultProjectID,
		Name: "Revision Ref", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	script := httpScript("revision-ref-release-script", "Revision ref release")
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	source, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Release source", Duration: "1m", ScriptID: script.ID, VUsers: 1,
		ApplicationID: "revision-ref-app", ReleaseID: "revision-ref-release",
	})
	if err != nil {
		t.Fatalf("CreateTest(source): %v", err)
	}
	if source.RefKey == "" {
		t.Fatal("source ref key is empty")
	}

	revision, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Release revision", RevisionSourceTestID: source.RefKey,
		Groups: createGroupEntriesFromManagedTest(source),
	})
	if err != nil {
		t.Fatalf("CreateTest(revision by ref key): %v", err)
	}
	if revision.ApplicationID != source.ApplicationID || revision.ReleaseID != source.ReleaseID {
		t.Fatalf("revision release context = %q/%q, want source %q/%q", revision.ApplicationID, revision.ReleaseID, source.ApplicationID, source.ReleaseID)
	}
}

func TestQueuedRunRevisionUsesSnapshotWhenScenarioCacheEntryIsMissing(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := httpScript("revision-missing-scenario-script", "Missing scenario source")
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Scenario with stale controller cache", Duration: "1m",
		Groups: []CreateGroupEntry{{Name: "Primary", ScriptID: script.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	source, err := server.runner.CreateTest(CreateTestRequest{Name: "Queued source", ScenarioID: scenario.ID})
	if err != nil {
		t.Fatalf("CreateTest(source): %v", err)
	}
	// Supported scenario deletion refuses linked tests. Remove only the cached
	// controller entry to prove a complete queued snapshot does not depend on a
	// fresh scenario-cache load while it is being revised.
	server.runner.mu.Lock()
	delete(server.runner.scenarios, scenario.ID)
	server.runner.mu.Unlock()

	revision, err := server.runner.CreateTest(CreateTestRequest{
		Name:                 "Revision from surviving snapshot",
		RevisionSourceTestID: source.ID,
		ScenarioID:           source.ScenarioID,
		Groups:               createGroupEntriesFromManagedTest(source),
	})
	if err != nil {
		t.Fatalf("CreateTest(revision): %v", err)
	}
	if revision.ScenarioID != source.ScenarioID || revision.ProjectID != source.ProjectID {
		t.Fatalf("revision identity = scenario %q project %q, want source %q/%q", revision.ScenarioID, revision.ProjectID, source.ScenarioID, source.ProjectID)
	}
	if got, want := revision.groupSpecs[0].Script.Actions[0].Target, source.groupSpecs[0].Script.Actions[0].Target; got != want {
		t.Fatalf("revision action target = %q, want immutable source %q", got, want)
	}
}

func TestQueuedRunRevisionRejectsNonQueuedAndCrossProjectSources(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.tests["running-source"] = &ManagedTest{ID: "running-source", Status: "RUNNING", ProjectID: "project-a"}
	runner.mu.RLock()
	_, err := runner.revisionSourceTestLocked(&CreateTestRequest{RevisionSourceTestID: "running-source", ProjectID: "project-a"})
	runner.mu.RUnlock()
	if err == nil || !strings.Contains(err.Error(), "must be QUEUED") {
		t.Fatalf("non-queued revision source error = %v", err)
	}

	runner.tests["queued-source"] = &ManagedTest{ID: "queued-source", Status: "QUEUED", ProjectID: "project-b"}
	runner.mu.RLock()
	_, err = runner.revisionSourceTestLocked(&CreateTestRequest{RevisionSourceTestID: "queued-source", ProjectID: "project-a"})
	runner.mu.RUnlock()
	if err == nil || !strings.Contains(err.Error(), "one project") {
		t.Fatalf("cross-project revision source error = %v", err)
	}
}

func TestRevisionSourceGroupIndexPreventsCredentialSwap(t *testing.T) {
	source := &ManagedTest{Groups: []TestGroup{
		{ScriptID: "shared", Name: "A", RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"TOKEN": "secret-a"}}},
		{ScriptID: "shared", Name: "B", RuntimeSettings: &RuntimeSettingsOverride{Env: map[string]string{"TOKEN": "secret-b"}}},
	}}
	first, second := 0, 1
	groups := []CreateGroupEntry{
		{Name: "B", ScriptID: "shared", RevisionSourceGroupIndex: &first},
		{Name: "A", ScriptID: "shared", RevisionSourceGroupIndex: &second},
	}
	if err := validateRevisionSourceGroupIndexes(groups, source); err != nil {
		t.Fatalf("validate indexed rename: %v", err)
	}
	if got := existingManagedTestGroupRuntime(source, groups[0]).Env["TOKEN"]; got != "secret-a" {
		t.Fatalf("first renamed group credential = %q, want secret-a", got)
	}
	if got := existingManagedTestGroupRuntime(source, groups[1]).Env["TOKEN"]; got != "secret-b" {
		t.Fatalf("second renamed group credential = %q, want secret-b", got)
	}
	if err := validateRevisionSourceGroupIndexes([]CreateGroupEntry{{Name: "C", ScriptID: "shared", RevisionNewGroup: true}}, source); err != nil {
		t.Fatalf("explicit new same-script group rejected: %v", err)
	}
}

func TestRevisionDataPoolIntentIsOrderInsensitiveAndNormalizesDefaults(t *testing.T) {
	now := time.Now().UTC()
	source := []storage.ScenarioDataPoolBinding{
		{
			ID: "binding-orders", ScenarioID: "scenario-id", PoolID: "pool-orders", PoolName: "Orders display name",
			Alias: "orders", AccessMode: "consume", SelectionStrategy: "unique", CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "binding-customers", ScenarioID: "scenario-id", PoolID: "pool-customers", PoolName: "Customers display name",
			Alias: "customers", AccessMode: "read_write", SelectionStrategy: "sequential", CreatedAt: now, UpdatedAt: now,
		},
	}
	// The client reordered the bindings, omitted canonical defaults, and retained
	// harmless input whitespace. None of those changes should trigger a live row
	// reload for a queued-run revision.
	minimalClientPayload := []storage.ScenarioDataPoolBinding{
		{PoolID: " pool-customers ", Alias: " CUSTOMERS ", AccessMode: " ", SelectionStrategy: ""},
		{PoolID: "pool-orders", Alias: " Orders ", AccessMode: " read_only ", SelectionStrategy: " unique "},
	}
	if !sameRevisionDataPoolBindings(minimalClientPayload, source) {
		t.Fatal("reordered/default-equivalent bindings were treated as an intentional data-pool edit")
	}
	minimalClientPayload[1].SelectionStrategy = "random"
	if sameRevisionDataPoolBindings(minimalClientPayload, source) {
		t.Fatal("execution-significant binding edit was treated as unchanged")
	}

	duplicates := []storage.ScenarioDataPoolBinding{
		{PoolID: "pool-customers", Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential"},
		{PoolID: "pool-orders", Alias: " Customers ", AccessMode: "read_only", SelectionStrategy: "unique"},
	}
	if sameRevisionDataPoolBindings(duplicates, source) {
		t.Fatal("duplicate normalized aliases were treated as a valid unchanged intent")
	}
}

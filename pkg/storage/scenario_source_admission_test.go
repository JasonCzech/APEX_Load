package storage

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func requireScenarioSources(t *testing.T, store *SQLiteStore, groups []ScenarioScript) *ScenarioSourceAuthoringSnapshot {
	t.Helper()
	result, err := store.GetScenarioSourceAuthoringSnapshot(groups)
	if err != nil || result == nil {
		t.Fatalf("source snapshot: %v", err)
	}
	return result
}
func sourceCandidate(old *ScenarioAuthoringSnapshot, create bool) (*Scenario, []ScenarioScript) {
	candidate := *old.Scenario
	candidate.Name = "Attempted candidate"
	candidate.RefKey = ""
	groups := append([]ScenarioScript(nil), old.Scripts...)
	if create {
		candidate.ID = "source-new"
		candidate.CreatedAt = time.Time{}
		for i := range groups {
			groups[i].ScenarioID = ""
		}
	}
	return &candidate, groups
}
func sourceCreatePools(old *ScenarioAuthoringSnapshot) []ScenarioDataPoolBinding {
	pools := append([]ScenarioDataPoolBinding(nil), old.DataPools...)
	for i := range pools {
		pools[i].ID = "source-new-" + pools[i].ID
		pools[i].ScenarioID = ""
	}
	return pools
}
func trySourceMutation(store *SQLiteStore, old *ScenarioAuthoringSnapshot, create bool, admission ScenarioSourceAdmission) (*ScenarioAuthoringMutationResult, error) {
	candidate, groups := sourceCandidate(old, create)
	if create {
		return store.CreateScenarioAuthoringWithSourcesIfAbsent(candidate, groups, sourceCreatePools(old), admission)
	}
	return store.UpdateScenarioAuthoringWithSourcesIfCurrent(candidate, groups, old.DataPools, scenarioAuthoringExpected(old), admission)
}
func assertSourceMutationRefused(t *testing.T, store *SQLiteStore, old *ScenarioAuthoringSnapshot, admission ScenarioSourceAdmission) {
	t.Helper()
	before := requireScenarioAuthoring(t, store, old.Scenario.ID)
	for _, create := range []bool{true, false} {
		result, err := trySourceMutation(store, old, create, admission)
		if err == nil || result != nil {
			t.Fatalf("create=%v accepted result=%#v error=%v", create, result, err)
		}
		if !reflect.DeepEqual(requireScenarioAuthoring(t, store, old.Scenario.ID), before) {
			t.Fatal("refused save changed scenario/nonce/children")
		}
	}
	if _, err := store.GetScenarioAuthoringSnapshot("source-new"); !errors.Is(err, ErrScenarioAuthoringNotFound) {
		t.Fatalf("refused create installed candidate: %v", err)
	}
}

func exerciseScenarioSourceAdmissionHappy(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	sources := requireScenarioSources(t, store, old.Scripts)
	if len(sources.Sources) != 1 || len(sources.Admission.Sources) != 1 || sources.Admission.ProjectID != old.ProjectID {
		t.Fatal("invalid source evidence")
	}
	sources.Sources[0].Script.Name = "Local mutation"
	durable, err := store.GetScript(sources.Admission.Sources[0].ScriptID)
	if err != nil || durable.Name == "Local mutation" {
		t.Fatal("shared DSL mutation", err)
	}
	candidate, groups := sourceCandidate(old, true)
	groups = append(groups, groups[0])
	groups[1].Name = "Repeated source"
	evidence := sources.Admission
	before := append([]ScenarioSourceCondition(nil), evidence.Sources...)
	originalCandidate := *candidate
	created, err := store.CreateScenarioAuthoringWithSourcesIfAbsent(candidate, groups, sourceCreatePools(old), evidence)
	if err != nil || !created.Applied || len(created.Snapshot.Scripts) != 2 {
		t.Fatalf("create %#v %v", created, err)
	}
	if !reflect.DeepEqual(*candidate, originalCandidate) || !reflect.DeepEqual(evidence.Sources, before) || groups[0].ScenarioID != "" || groups[1].Order != 0 {
		t.Fatal("caller inputs mutated")
	}
	if created.Snapshot.Scripts[0].Stages == nil || created.Snapshot.Scripts[0].Iterations == nil || *created.Snapshot.Scripts[0].Iterations != 0 || created.Snapshot.Scripts[0].TransactionResponseTimeSLAMs == nil || *created.Snapshot.Scripts[0].TransactionResponseTimeSLAMs != 0 {
		t.Fatal("explicit empty/zero override clone lost")
	}
	// A create collision must not adopt the winner or issue its receipt.
	attempted := *candidate
	attempted.Name = "Collision"
	result, err := store.CreateScenarioAuthoringWithSourcesIfAbsent(&attempted, groups, old.DataPools, evidence)
	if result != nil || !errors.Is(err, ErrScenarioSourceAdmissionConflict) {
		t.Fatalf("collision %#v %v", result, err)
	}
	if requireScenarioAuthoring(t, store, candidate.ID).Scenario.Name != "Attempted candidate" {
		t.Fatal("collision replaced winner")
	}
	changed := *old.Scenario
	changed.Name = "Updated"
	result, err = store.UpdateScenarioAuthoringWithSourcesIfCurrent(&changed, old.Scripts, old.DataPools, scenarioAuthoringExpected(old), evidence)
	if err != nil || !result.Applied || result.Snapshot.Scenario.Name != "Updated" {
		t.Fatal("update", err)
	}
	// Ordinary low-level empty-scenario behavior stays available.
	result, err = store.CreateScenarioAuthoringIfAbsent(&Scenario{ID: "ordinary-empty", Name: "Empty", Duration: "1m"}, nil, nil)
	if err != nil || !result.Applied {
		t.Fatal("ordinary empty behavior changed", err)
	}
}
func TestScenarioSourceAdmissionHappy(t *testing.T) {
	exerciseScenarioSourceAdmissionHappy(t, newScenarioRevisionTestStore(t))
}

func exerciseScenarioSourcePublication(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	initial := requireScenarioSources(t, store, old.Scripts)
	sourceBefore, err := store.GetScriptAuthoringSnapshot(old.Scripts[0].ScriptID)
	if err != nil {
		t.Fatal(err)
	}
	pending := `[{"kind":" Scripts ","prepared":true,"local_item_id":" scn-script ","remote_id":" direct-import:prepared ","expires_at":"2000-01-01T00:00:00Z"}]`
	if applied, err := store.CompareAndSwapLibraryImportClaims("", pending); err != nil || !applied {
		t.Fatal(applied, err)
	}
	sourceAfter, err := store.GetScriptAuthoringSnapshot(old.Scripts[0].ScriptID)
	if err != nil || !reflect.DeepEqual(sourceBefore, sourceAfter) {
		t.Fatal("publication changed source", err)
	}
	assertSourceMutationRefused(t, store, old, initial.Admission)
	if result, err := store.GetScenarioSourceAuthoringSnapshot(old.Scripts); result != nil || !errors.Is(err, ErrScenarioSourceAdmissionConflict) {
		t.Fatal("initial unpublished source accepted", err)
	}
	// Even a caller capturing the exact current hash cannot authorize a source
	// that is presently covered by a prepared uncommitted claim.
	current := initial.Admission
	current.Publication = requirePublication(t, store).State
	assertSourceMutationRefused(t, store, old, current)
	committed := strings.Replace(pending, `"prepared":true`, `"prepared":true,"committed":true`, 1)
	if applied, err := store.CompareAndSwapLibraryImportClaims(pending, committed); err != nil || !applied {
		t.Fatal(applied, err)
	}
	fresh := requireScenarioSources(t, store, old.Scripts)
	if _, err := trySourceMutation(store, old, false, fresh.Admission); err != nil {
		t.Fatal("committed source unavailable", err)
	}
	old = requireScenarioAuthoring(t, store, old.Scenario.ID)
	// Publication changes that leave this scenario's sources published do not
	// invalidate a captured admission: the final transaction rechecks the
	// sources themselves under the publication lock.
	if applied, err := store.CompareAndSwapLibraryImportClaims(committed, "[]"); err != nil || !applied {
		t.Fatal(applied, err)
	}
	if _, err := trySourceMutation(store, old, false, fresh.Admission); err != nil {
		t.Fatal("a cleared claim invalidated the admission", err)
	}
	old = requireScenarioAuthoring(t, store, old.Scenario.ID)
	unrelated := `[{"kind":"result","prepared":true,"local_item_id":"other","remote_id":"direct-import:unrelated","owner":"takeover"}]`
	if applied, err := store.CompareAndSwapLibraryImportClaims("[]", unrelated); err != nil || !applied {
		t.Fatal(applied, err)
	}
	if _, err := trySourceMutation(store, old, true, fresh.Admission); err != nil {
		t.Fatal("an unrelated pending claim invalidated the admission", err)
	}
}
func TestScenarioSourcePublication(t *testing.T) {
	exerciseScenarioSourcePublication(t, newScenarioRevisionTestStore(t))
}

func TestScenarioSourceAdmissionBoundedEvidence(t *testing.T) {
	store := newScenarioRevisionTestStore(t)
	old := seedScenarioRevision(t, store)
	initial := requireScenarioSources(t, store, old.Scripts)
	for _, mutate := range []func(*ScenarioSourceAdmission){
		func(a *ScenarioSourceAdmission) { a.Sources = nil },
		func(a *ScenarioSourceAdmission) { a.Sources = append(a.Sources, a.Sources[0]) },
		func(a *ScenarioSourceAdmission) { a.Sources[0].ScriptID = "foreign" },
		func(a *ScenarioSourceAdmission) { a.Sources[0].ProjectID = "scn-foreign" },
		func(a *ScenarioSourceAdmission) { a.Sources[0].CreatedAt = time.Time{} },
		func(a *ScenarioSourceAdmission) { a.Sources[0].ContentRevision = "invalid" },
		func(a *ScenarioSourceAdmission) { a.ProjectID = " scn-project " },
		func(a *ScenarioSourceAdmission) { a.Publication.SHA256 = strings.Repeat("A", 64) },
		func(a *ScenarioSourceAdmission) { a.Publication.SHA256 = "short" },
	} {
		a := initial.Admission
		a.Sources = append([]ScenarioSourceCondition(nil), a.Sources...)
		mutate(&a)
		assertSourceMutationRefused(t, store, old, a)
	}
	candidate, _ := sourceCandidate(old, true)
	for _, bad := range [][]ScenarioScript{nil, {{ScriptID: " scn-script "}}, make([]ScenarioScript, maxScenarioSourceGroups+1)} {
		if _, err := store.GetScenarioSourceAuthoringSnapshot(bad); err == nil {
			t.Fatal("invalid groups accepted initially")
		}
		if _, err := store.CreateScenarioAuthoringWithSourcesIfAbsent(candidate, bad, nil, initial.Admission); err == nil {
			t.Fatal("invalid groups accepted finally")
		}
	}
	groups := make([]ScenarioScript, maxScenarioSourceGroups)
	for i := range groups {
		groups[i] = ScenarioScript{ScriptID: old.Scripts[0].ScriptID}
	}
	if _, err := store.GetScenarioSourceAuthoringSnapshot(groups); err != nil {
		t.Fatal("exact group limit", err)
	}
}

func exerciseScenarioSourceChanged(t *testing.T, store *SQLiteStore) {
	old := seedScenarioRevision(t, store)
	initial := requireScenarioSources(t, store, old.Scripts)
	id := old.Scripts[0].ScriptID
	before, err := store.GetScript(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE scripts SET name=?,updated_at=? WHERE id=?`, "Changed source defaults", before.UpdatedAt, id); err != nil {
		t.Fatal(err)
	}
	if rawScenarioRevision(t, store, old.Scenario.ID) != old.ContentRevision {
		t.Fatal("source defaults changed scenario nonce")
	}
	assertSourceMutationRefused(t, store, old, initial.Admission)
	initial = requireScenarioSources(t, store, old.Scripts)
	if _, err := store.db.Exec(`UPDATE scripts SET created_at=? WHERE id=?`, before.CreatedAt.Add(time.Hour), id); err != nil {
		t.Fatal(err)
	}
	assertSourceMutationRefused(t, store, old, initial.Admission)
	fresh := requireScenarioSources(t, store, old.Scripts)
	if _, err := store.db.Exec(`UPDATE scripts SET project_id=? WHERE id=?`, "scn-foreign", id); err != nil {
		t.Fatal(err)
	}
	assertSourceMutationRawRefused(t, store, old, fresh.Admission)
}
func TestScenarioSourceChanged(t *testing.T) {
	exerciseScenarioSourceChanged(t, newScenarioRevisionTestStore(t))
}

func TestScenarioSourcePublicationMalformedAndPresence(t *testing.T) {
	for _, raw := range []string{`[{"kind":"script","prepared":null}]`, `[{"kind":"script","prepared":true,"PREPARED":false}]`, `[null]`, `[{"kind":"unknown"}]`} {
		t.Run(raw, func(t *testing.T) {
			store := newScenarioRevisionTestStore(t)
			old := seedScenarioRevision(t, store)
			initial := requireScenarioSources(t, store, old.Scripts)
			if err := store.SaveSetting(LibraryImportClaimsSettingKey, raw); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetScenarioSourceAuthoringSnapshot(old.Scripts); err == nil {
				t.Fatal("malformed initial publication accepted")
			}
			initial.Admission.Publication = requirePublication(t, store).State
			assertSourceMutationRefused(t, store, old, initial.Admission)
		})
	}
	store := newScenarioRevisionTestStore(t)
	old := seedScenarioRevision(t, store)
	initial := requireScenarioSources(t, store, old.Scripts)
	if err := store.SaveSetting(LibraryImportClaimsSettingKey, ""); err != nil {
		t.Fatal(err)
	}
	// Row presence alone does not unpublish a source, so the admission
	// captured before it still stands.
	if _, err := trySourceMutation(store, old, false, initial.Admission); err != nil {
		t.Fatal("a presence-only publication change invalidated the admission", err)
	}
	current := requireScenarioSources(t, store, old.Scripts)
	if current.Admission.Publication.SHA256 != initial.Admission.Publication.SHA256 || !current.Admission.Publication.Exists {
		t.Fatal("row presence collapsed")
	}
	if _, err := trySourceMutation(store, old, true, current.Admission); err != nil {
		t.Fatal("present empty current evidence", err)
	}
}

package api

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// scenarioAuthoredWriterMethods are the storage and runner calls that change
// scenario-authored state (the scenario row, its groups, data-pool bindings,
// library placement or monitor selection).
var scenarioAuthoredWriterMethods = map[string]bool{
	"SaveScenario": true, "SaveScenarioWithDataPoolBindings": true, "SaveScenarioDataPoolBindings": true,
	"DeleteScenario": true, "DeleteScenarioGuarded": true,
	"UpdateScenarioDataPoolBindingsIfUnchanged": true, "UpdateScenarioDataPoolBindingsGuarded": true,
	"UpdateScenarioWithDataPoolBindingsIfUnchanged": true, "UpdateScenarioWithDataPoolBindingsGuarded": true,
	"SaveMonitorBindings": true, "SaveMonitorBindingsGuarded": true,
	"ReplaceLibraryAssignment": true, "ReplaceScenarioLibraryAssignmentGuarded": true,
	"UpdateScenarioAuthoringIfCurrent": true, "UpdateScenarioAuthoringWithSourcesIfCurrent": true,
	"DeleteProject": true,
	"StoreScenario": true, "CreateScenarioAuthoringDurable": true, "UpdateScenario": true, "UpdateScenarioIfUnchanged": true,
	"UpdateScenarioAuthoringDurable": true, "DeleteScenarioDurable": true, "ApplyScenarioRuntimeToGroups": true,
}

// scenarioAuthoredWriterCallers classifies every function that reaches one of
// those calls. Entries describe how the write is fenced against a concurrent
// authoring session.
var scenarioAuthoredWriterCallers = map[string]string{
	"Server.handleUpdateScenarioDataPools":         "guarded writer: optional If-Match, strict policy",
	"Server.handleLibraryAssignment":               "guarded writer for scenarios; script placement keeps its own CAS",
	"Server.handleMonitorBindings":                 "guarded writer: optional If-Match, strict policy",
	"Server.handleApplyScenarioRuntimeToGroups":    "guarded writer: optional If-Match, strict policy",
	"Server.handleDeleteScenario":                  "guarded writer: optional If-Match, strict policy",
	"Server.handleUpdateScenario":                  "ordinary PUT: conditioned, or an audited legacy save the strict policy refuses",
	"Server.handleCreateScenario":                  "creates a new scenario; no prior authored state",
	"Server.importLibraryScenario":                 "library import and remote pull replace an imported copy under its updated_at CAS; the copy is owned by its source library, outside the strict-policy model",
	"libraryImportJournal.recordScenario":          "import rollback restores the pre-import scenario under its updated_at CAS",
	"Server.deleteProjectDurably":                  "project deletion removes every scenario of the project; no authoring session survives it",
	"Server.clearProjectBrunoImportSessions":       "Bruno object-store cleanup named DeleteProject; touches no scenario state",
	"TestRunner.ApplyScenarioRuntimeToGroups":      "runner side of the guarded apply-to-groups writer",
	"TestRunner.DeleteScenarioDurable":             "runner side of the guarded delete",
	"TestRunner.DeleteScenario":                    "unconditioned runner delete kept for tests and internal callers; no route reaches it",
	"TestRunner.storeScenarioLockedWithSources":    "save builder shared by create, PUT and import; each caller supplies its own fence",
	"TestRunner.backfillScenarioRefKeys":           "server-owned ref-key backfill at startup; not authored content",
	"TestRunner.repairLegacyStoredScenarioRuntime": "startup repair of legacy runtime credentials under the updated_at CAS",
	"updateScenarioAggregateIfUnchanged":           "updated_at CAS helper for legacy-adapter saves",
	"updateScenarioAuthoringAggregate":             "final write of the conditioned PUT",
}

func scenarioWriterCallerName(decl *ast.FuncDecl) string {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return decl.Name.Name
	}
	recv := decl.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return ident.Name + "." + decl.Name.Name
	}
	return decl.Name.Name
}

// Classifying by reachable storage writers, not by URL, catches writers such
// as library import, remote pull and project deletion that live outside the
// /api/v1/scenarios/{id} prefix.
func TestEveryScenarioAuthoredWriterCallerIsClassified(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	found := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			caller := scenarioWriterCallerName(fn)
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok && scenarioAuthoredWriterMethods[selector.Sel.Name] {
					found[caller] = true
				}
				return true
			})
		}
	}
	var unclassified, stale []string
	for caller := range found {
		if _, ok := scenarioAuthoredWriterCallers[caller]; !ok {
			unclassified = append(unclassified, caller)
		}
	}
	for caller := range scenarioAuthoredWriterCallers {
		if !found[caller] {
			stale = append(stale, caller)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	if len(unclassified) > 0 || len(stale) > 0 {
		t.Fatalf("unclassified scenario writers %v; classified functions that no longer write %v", unclassified, stale)
	}
}

type guardedScenarioWriterCase struct {
	route string
	path  string
	body  any
}

func guardedScenarioWriterCases(id string) []guardedScenarioWriterCase {
	base := "/api/v1/scenarios/" + id
	return []guardedScenarioWriterCase{
		{"PUT /api/v1/scenarios/{id}/data-pools", base + "/data-pools", map[string]any{"data_pools": []any{}}},
		{"PUT /api/v1/scenarios/{id}/library-assignment", base + "/library-assignment", map[string]any{"application_id": nil, "release_ids": []string{}, "expected_revision": 0}},
		{"PUT /api/v1/scenarios/{id}/monitor-bindings", base + "/monitor-bindings", map[string]any{}},
		{"POST /api/v1/scenarios/{id}/runtime/apply-to-groups", base + "/runtime/apply-to-groups", map[string]any{"source_group": 0, "target_groups": []int{}, "fields": []string{"pacing"}}},
		{"DELETE /api/v1/scenarios/{id}", base, nil},
	}
}

func TestGuardedScenarioWritersHonorIfMatchAndStrictPolicy(t *testing.T) {
	covered := map[string]bool{}
	for _, probe := range guardedScenarioWriterCases("probe") {
		covered[probe.route] = true
	}
	if !reflect.DeepEqual(covered, scenarioGuardedWriterRoutes) {
		t.Fatalf("guarded writer cases %v differ from the classified routes", covered)
	}
	for index := range guardedScenarioWriterCases("probe") {
		t.Run(guardedScenarioWriterCases("probe")[index].route, func(t *testing.T) {
			t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", storage.DefaultProjectID)
			server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
			writer := guardedScenarioWriterCases(scenario.ID)[index]
			method, _, _ := strings.Cut(writer.route, " ")
			_, stale, _ := requireScenarioAuthoringView(t, server, scenario.ID)
			// Another editor saves; the first tag is now stale.
			if response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Saved elsewhere", 2), stale); response.Code != http.StatusOK {
				t.Fatalf("competing save: %d %s", response.Code, response.Body.String())
			}
			before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			unchanged := func(step string) {
				t.Helper()
				if after, err := store.GetScenarioAuthoringSnapshot(scenario.ID); err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("%s mutated the scenario: %v", step, err)
				}
			}
			if response := authoringAPIRequest(t, server, method, writer.path, writer.body, stale); response.Code != http.StatusPreconditionFailed {
				t.Fatalf("stale tag: %d %s", response.Code, response.Body.String())
			}
			unchanged("stale tag")
			if response := authoringAPIRequest(t, server, method, writer.path, writer.body, `W/"scenario-authoring-v1:x"`); response.Code != http.StatusBadRequest {
				t.Fatalf("weak tag: %d %s", response.Code, response.Body.String())
			}
			unchanged("weak tag")
			setScenarioStrictPolicy(t, store, true)
			if response := authoringAPIRequest(t, server, method, writer.path, writer.body, ""); response.Code != http.StatusPreconditionRequired {
				t.Fatalf("strict policy without a tag: %d %s", response.Code, response.Body.String())
			}
			unchanged("strict policy")
			current, err := scenarioAuthoringETag(before.ContentRevision)
			if err != nil {
				t.Fatal(err)
			}
			response := authoringAPIRequest(t, server, method, writer.path, writer.body, current)
			if response.Code >= 300 {
				t.Fatalf("current tag: %d %s", response.Code, response.Body.String())
			}
			if method == http.MethodDelete {
				if _, err := store.GetScenarioAuthoringSnapshot(scenario.ID); err == nil {
					t.Fatal("conditioned delete left the scenario")
				}
				return
			}
			after, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			if want, _ := scenarioAuthoringETag(after.ContentRevision); response.Header().Get("ETag") != want {
				t.Fatalf("ETag %q, want the committed %q", response.Header().Get("ETag"), want)
			}
		})
	}
}

// The policy is read inside each writer's own transaction, so a policy enabled
// after admission still refuses the unconditioned write.
func TestGuardedScenarioWriterRereadsStrictPolicyInTransaction(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	admission, admitted := server.admitScenarioWriter(httptest.NewRecorder(), httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/data-pools", nil), scenario.ID, scenario.ProjectID)
	if !admitted || !admission.guarded() || admission.guard.Expected != nil {
		t.Fatalf("unconditioned admission %#v %v", admission, admitted)
	}
	setScenarioStrictPolicy(t, store, true)
	before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = admission.store.UpdateScenarioDataPoolBindingsGuarded(scenario.ID, nil, before.Scenario.UpdatedAt, admission.guard)
	if err == nil || !strings.Contains(err.Error(), storage.ErrScenarioAuthoringPreconditionRequired.Error()) {
		t.Fatalf("policy flip after admission: %v", err)
	}
	if after, err := store.GetScenarioAuthoringSnapshot(scenario.ID); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused write mutated the scenario: %v", err)
	}
}

// scenarioRaceBarrierStore holds each conditioned write until both replicas
// have passed admission, so the race is decided by the storage CAS rather than
// by request ordering.
type scenarioRaceBarrierStore struct {
	SQLiteStore *storage.SQLiteStore
	arrive      func()
}

func (s *scenarioRaceBarrierStore) UpdateScenarioAuthoringWithSourcesIfCurrent(scenario *storage.Scenario, groups []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding, expected storage.ScenarioAuthoringPrecondition, admission storage.ScenarioSourceAdmission) (*storage.ScenarioAuthoringMutationResult, error) {
	s.arrive()
	return s.SQLiteStore.UpdateScenarioAuthoringWithSourcesIfCurrent(scenario, groups, pools, expected, admission)
}

func newScenarioRaceBarrier(t *testing.T, parties int) func() {
	var mu sync.Mutex
	arrived := 0
	release := make(chan struct{})
	return func() {
		mu.Lock()
		arrived++
		if arrived == parties {
			close(release)
		}
		mu.Unlock()
		select {
		case <-release:
		case <-time.After(20 * time.Second):
			t.Error("replica never reached the conditioned write")
		}
	}
}

// exerciseScenarioReplicaRace sends the same validator from two replicas. Both
// pass admission and their cache checks; exactly one commits and the other is
// refused by the durable revision CAS.
func exerciseScenarioReplicaRace(t *testing.T, owner, peer *Server, ownerStore, peerStore *storage.SQLiteStore, scenarioID string, request func(string, int) CreateScenarioRequest) {
	t.Helper()
	_, tag, _ := requireScenarioAuthoringView(t, owner, scenarioID)
	if _, peerTag, _ := requireScenarioAuthoringView(t, peer, scenarioID); peerTag != tag {
		t.Fatal("replicas disagree on the baseline")
	}
	arrive := newScenarioRaceBarrier(t, 2)
	owner.runner.scenarioStore = sqliteOverlay(&scenarioRaceBarrierStore{SQLiteStore: ownerStore, arrive: arrive})
	peer.runner.scenarioStore = sqliteOverlay(&scenarioRaceBarrierStore{SQLiteStore: peerStore, arrive: arrive})
	defer func() { owner.runner.scenarioStore, peer.runner.scenarioStore = ownerStore, peerStore }()
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	for index, server := range []*Server{owner, peer} {
		wg.Add(1)
		go func(index int, server *Server) {
			defer wg.Done()
			response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenarioID, request(fmt.Sprintf("Replica %d", index), index+2), tag)
			codes <- response.Code
		}(index, server)
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for code := range codes {
		got[code]++
	}
	if got[http.StatusOK] != 1 || got[http.StatusPreconditionFailed] != 1 {
		t.Fatalf("replica race outcomes %v, want one 200 and one 412", got)
	}
}

func TestScenarioConditionedSaveRaceAcrossSQLiteReplicas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replicas.db")
	open := func() (*Server, *storage.SQLiteStore) {
		store, err := storage.NewSQLiteStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		return NewServer(ServerConfig{Store: store}), store
	}
	owner, ownerStore := open()
	owner.runner.StoreScript(projectSecurityScript("scenario-policy-script", storage.DefaultProjectID))
	scenario, err := owner.runner.StoreScenario(scenarioSaveRequest("Original", 1))
	if err != nil {
		t.Fatal(err)
	}
	peer, peerStore := open()
	exerciseScenarioReplicaRace(t, owner, peer, ownerStore, peerStore, scenario.ID, scenarioSaveRequest)
}

// sourceEditedDuringSaveStore stands in for another replica that edits a group
// source after this save compiled it but before the write commits.
type sourceEditedDuringSaveStore struct {
	SQLiteStore *storage.SQLiteStore
	t           *testing.T
}

func (s *sourceEditedDuringSaveStore) UpdateScenarioAuthoringWithSourcesIfCurrent(scenario *storage.Scenario, groups []storage.ScenarioScript, pools []storage.ScenarioDataPoolBinding, expected storage.ScenarioAuthoringPrecondition, admission storage.ScenarioSourceAdmission) (*storage.ScenarioAuthoringMutationResult, error) {
	script, err := s.SQLiteStore.GetScript(groups[0].ScriptID)
	if err != nil {
		s.t.Fatal(err)
	}
	script.Name = "Edited on another replica"
	if err := s.SQLiteStore.SaveScript(script); err != nil {
		s.t.Fatal(err)
	}
	return s.SQLiteStore.UpdateScenarioAuthoringWithSourcesIfCurrent(scenario, groups, pools, expected, admission)
}

// The conditioned save compiles from durable sources and its write claims
// their revisions, so a source edited between compilation and commit refuses
// the save instead of committing a scenario validated against old content.
func TestScenarioSaveClaimsCompiledSourceRevisions(t *testing.T) {
	server, store, secrets, scenario := newScenarioPolicyAvailabilityFixture(t)
	_, tag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	server.runner.scenarioStore = sqliteOverlay(&sourceEditedDuringSaveStore{SQLiteStore: store, t: t})
	response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Compiled against old source", 2), tag)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "script changed") {
		t.Fatalf("source edited during save: %d %s", response.Code, response.Body.String())
	}
	requireNoScenarioSaveSideEffects(t, store, secrets, before)
}

// Sources archived or left unpublished by another replica never reach a
// committed save, whichever gate refuses them first.
func TestScenarioSaveAdmitsDurableSources(t *testing.T) {
	t.Run("archived", func(t *testing.T) {
		server, store, secrets, scenario := newScenarioPolicyAvailabilityFixture(t)
		_, tag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
		script, err := store.GetScript("scenario-policy-script")
		if err != nil {
			t.Fatal(err)
		}
		if archived, err := store.SetObjectArchived("script", script.ID, script.UpdatedAt, true); err != nil || !archived {
			t.Fatalf("archive: %v %v", archived, err)
		}
		before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
		if err != nil {
			t.Fatal(err)
		}
		response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Archived source", 2), tag)
		if response.Code < 400 {
			t.Fatalf("archived source accepted: %d %s", response.Code, response.Body.String())
		}
		requireNoScenarioSaveSideEffects(t, store, secrets, before)
	})
	t.Run("unpublished", func(t *testing.T) {
		server, store, secrets, scenario := newScenarioPolicyAvailabilityFixture(t)
		_, tag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
		pending := `[{"kind":"scripts","prepared":true,"local_item_id":"scenario-policy-script","remote_id":"direct-import:prepared","expires_at":"2999-01-01T00:00:00Z"}]`
		if applied, err := store.CompareAndSwapLibraryImportClaims("", pending); err != nil || !applied {
			t.Fatalf("claim: %v %v", applied, err)
		}
		before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
		if err != nil {
			t.Fatal(err)
		}
		response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scenarios/"+scenario.ID, scenarioSaveRequest("Unpublished source", 2), tag)
		if response.Code < 400 {
			t.Fatalf("unpublished source accepted: %d %s", response.Code, response.Body.String())
		}
		requireNoScenarioSaveSideEffects(t, store, secrets, before)
	})
}

// A secondary writer admitted with a current tag still compares it inside its
// own transaction: another writer committing in between makes it stale.
func TestGuardedScenarioWriterComparesItsTagInTransaction(t *testing.T) {
	server, store, _, scenario := newScenarioPolicyAvailabilityFixture(t)
	_, tag, _ := requireScenarioAuthoringView(t, server, scenario.ID)
	request := httptest.NewRequest(http.MethodPut, "/api/v1/scenarios/"+scenario.ID+"/data-pools", nil)
	request.Header.Set("If-Match", tag)
	admission, admitted := server.admitScenarioWriter(httptest.NewRecorder(), request, scenario.ID, scenario.ProjectID)
	if !admitted || !admission.guarded() || admission.guard.Expected == nil {
		t.Fatalf("conditioned admission %#v %v", admission, admitted)
	}
	snapshot, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	edited := *snapshot.Scenario
	edited.Name = "Saved between admission and write"
	if _, err := store.UpdateScenarioAuthoringIfCurrent(&edited, snapshot.Scripts, snapshot.DataPools, storage.ScenarioAuthoringPrecondition{ProjectID: snapshot.ProjectID, ContentRevision: snapshot.ContentRevision, CreatedAt: snapshot.Scenario.CreatedAt}); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetScenarioAuthoringSnapshot(scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = admission.store.UpdateScenarioDataPoolBindingsGuarded(scenario.ID, nil, before.Scenario.UpdatedAt, admission.guard)
	if !errors.Is(err, storage.ErrScenarioAuthoringConflict) {
		t.Fatalf("a stale tag was not refused in the write transaction: %v", err)
	}
	if after, err := store.GetScenarioAuthoringSnapshot(scenario.ID); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("refused write mutated the scenario: %v", err)
	}
}

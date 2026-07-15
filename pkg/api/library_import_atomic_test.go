package api

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// libraryAtomicForwardingStore deliberately embeds only storage.Store and
// forwards the extension interfaces used by atomic library imports. Tests can
// then override one durable boundary without accidentally disabling the rest.
type libraryAtomicForwardingStore struct {
	storage.Store
}

func newLibraryAtomicTestServerAfterStartupRecovery(t *testing.T, store storage.Store) *Server {
	t.Helper()
	now := time.Now().UTC()
	claim := libraryImportClaim{
		Identity: libraryLinkKey("startup", storage.DefaultProjectID, "script", "startup", "startup"),
		OwnerID:  "startup-owner", Sequence: 0, RemoteID: "startup", ProjectID: storage.DefaultProjectID,
		Kind: "script", SourceInstanceID: "startup", SourceItemID: "startup", MutationID: "startup-mutation",
		IntendedHash: "startup-hash", Committed: true, CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	payload, err := json.Marshal([]libraryImportClaim{claim})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSetting(libraryImportClaimsSettingKey, string(payload)); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	// Server.Start performs this recovery synchronously before launching any
	// listener or background worker. These storage-focused tests intentionally
	// do not start a network listener, so exercise the same startup boundary
	// directly instead of waiting for the old NewServer goroutine.
	if err := server.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("startup library claim recovery failed: %v", err)
	}
	claims, err := server.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("startup library claim recovery did not finish: %#v", claims)
	}
	return server
}

func (s *libraryAtomicForwardingStore) CompareAndSwapSetting(key, expectedValue, nextValue string) (bool, error) {
	return s.Store.(interface {
		CompareAndSwapSetting(string, string, string) (bool, error)
	}).CompareAndSwapSetting(key, expectedValue, nextValue)
}

func (s *libraryAtomicForwardingStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	return s.Store.(interface {
		CreateScriptIfAbsent(*dsl.Script) (bool, error)
	}).CreateScriptIfAbsent(script)
}

func (s *libraryAtomicForwardingStore) UpdateScriptIfUnchanged(script *dsl.Script, expected time.Time) (bool, error) {
	return s.Store.(interface {
		UpdateScriptIfUnchanged(*dsl.Script, time.Time) (bool, error)
	}).UpdateScriptIfUnchanged(script, expected)
}

func (s *libraryAtomicForwardingStore) DeleteScriptIfUnchanged(id string, expected time.Time) (bool, error) {
	return s.Store.(interface {
		DeleteScriptIfUnchanged(string, time.Time) (bool, error)
	}).DeleteScriptIfUnchanged(id, expected)
}

func (s *libraryAtomicForwardingStore) CreateScenarioWithDataPoolBindingsIfAbsent(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding) (bool, error) {
	return s.Store.(interface {
		CreateScenarioWithDataPoolBindingsIfAbsent(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding) (bool, error)
	}).CreateScenarioWithDataPoolBindingsIfAbsent(scenario, scripts, bindings)
}

func (s *libraryAtomicForwardingStore) UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario *storage.Scenario, scripts []storage.ScenarioScript, bindings []storage.ScenarioDataPoolBinding, expected time.Time) (bool, error) {
	return s.Store.(interface {
		UpdateScenarioWithDataPoolBindingsIfUnchanged(*storage.Scenario, []storage.ScenarioScript, []storage.ScenarioDataPoolBinding, time.Time) (bool, error)
	}).UpdateScenarioWithDataPoolBindingsIfUnchanged(scenario, scripts, bindings, expected)
}

func (s *libraryAtomicForwardingStore) DeleteScenarioIfUnchanged(id string, expected time.Time) (bool, error) {
	return s.Store.(interface {
		DeleteScenarioIfUnchanged(string, time.Time) (bool, error)
	}).DeleteScenarioIfUnchanged(id, expected)
}

func (s *libraryAtomicForwardingStore) CreateExternalResultImportIfAbsent(result *storage.ExternalResultImport) (bool, error) {
	return s.Store.(interface {
		CreateExternalResultImportIfAbsent(*storage.ExternalResultImport) (bool, error)
	}).CreateExternalResultImportIfAbsent(result)
}

func (s *libraryAtomicForwardingStore) GetExternalResultImport(id string) (*storage.ExternalResultImport, error) {
	return s.Store.(interface {
		GetExternalResultImport(string) (*storage.ExternalResultImport, error)
	}).GetExternalResultImport(id)
}

func (s *libraryAtomicForwardingStore) DeleteExternalResultImportIfRawResult(id string, expected map[string]any) (bool, error) {
	return s.Store.(interface {
		DeleteExternalResultImportIfRawResult(string, map[string]any) (bool, error)
	}).DeleteExternalResultImportIfRawResult(id, expected)
}

func (s *libraryAtomicForwardingStore) DeleteExternalResultImportIfUnchanged(expected *storage.ExternalResultImport) (bool, error) {
	return s.Store.(interface {
		DeleteExternalResultImportIfUnchanged(*storage.ExternalResultImport) (bool, error)
	}).DeleteExternalResultImportIfUnchanged(expected)
}

type failLibraryResultCreateStore struct {
	*libraryAtomicForwardingStore
}

func (s *failLibraryResultCreateStore) CreateExternalResultImportIfAbsent(*storage.ExternalResultImport) (bool, error) {
	return false, errors.New("injected result evidence failure")
}

type failResultAndRollbackDeleteStore struct {
	*libraryAtomicForwardingStore
}

func (s *failResultAndRollbackDeleteStore) CreateExternalResultImportIfAbsent(*storage.ExternalResultImport) (bool, error) {
	return false, errors.New("injected result evidence failure")
}

func (s *failResultAndRollbackDeleteStore) DeleteScriptIfUnchanged(string, time.Time) (bool, error) {
	return false, errors.New("injected rollback delete failure")
}

func TestLibraryBundleRollsBackScriptsScenariosAndLinksWhenLaterResultFails(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "bundle-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer baseStore.Close()
	store := &failLibraryResultCreateStore{libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: baseStore}}
	server := NewServer(ServerConfig{Store: store})

	script := libraryTestScript("source-script", "Source Script", "source-project", "/source")
	scenarioRequest := CreateScenarioRequest{
		Name: "Source Scenario", Duration: "1m",
		Groups: []CreateGroupEntry{{Name: "source", ScriptID: script.ID, VUsers: 1}},
	}
	testRun := &ManagedTest{ID: "source-result", Name: "Source Result", Status: "COMPLETE", ProjectID: "source-project", DurationStr: "1m", TotalVUsers: 1}
	summary := libraryResultSummary(testRun)
	bundle := LibraryBundle{
		SchemaVersion: libraryBundleSchemaVersion,
		Source:        LibraryBundleSource{InstanceID: "source-instance", ProjectID: "source-project"},
		Items: []LibraryBundleItem{
			{Kind: "script", SourceInstanceID: "source-instance", SourceItemID: script.ID, ProjectID: "source-project", Name: script.Name, Script: script, ContentHash: hashLibraryScript(script)},
			{Kind: "scenario", SourceInstanceID: "source-instance", SourceItemID: "source-scenario", ProjectID: "source-project", Name: scenarioRequest.Name, Scenario: &LibraryScenarioPayload{ID: "source-scenario", Request: scenarioRequest}, ContentHash: hashCanonical(scenarioRequest), Dependencies: []LibraryDependency{{Kind: "script", ID: script.ID, ProjectID: "source-project", ContentHash: hashLibraryScript(script)}}},
			{Kind: "result", SourceInstanceID: "source-instance", SourceItemID: testRun.ID, ProjectID: "source-project", Name: testRun.Name, Result: &LibraryResultPayload{Test: testRun, Summary: summary}, ContentHash: hashLibraryResultWithSummary(testRun, summary), ContentHashVersion: libraryResultHashVersionCurrent},
		},
	}

	response, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bundle}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Conflicts) != 1 || !strings.Contains(response.Conflicts[0].Reason, "injected result evidence failure") {
		t.Fatalf("conflicts = %#v", response.Conflicts)
	}
	if len(response.Items) != 0 || len(response.Warnings) != 0 {
		t.Fatalf("partial success leaked from rolled-back response: %#v", response)
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("rolled-back scripts = %#v", scripts)
	}
	if scenarios := server.runner.ListScenarios(); len(scenarios) != 0 {
		t.Fatalf("rolled-back scenarios = %#v", scenarios)
	}
	imports, err := baseStore.ListExternalResultImports(storage.ExternalResultImportFilter{ProjectID: storage.DefaultProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 0 {
		t.Fatalf("rolled-back result imports = %#v", imports)
	}
	rawLinks, err := baseStore.GetSetting(librarySyncLinksSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	var links []librarySyncLink
	if strings.TrimSpace(rawLinks) != "" {
		if err := json.Unmarshal([]byte(rawLinks), &links); err != nil {
			t.Fatal(err)
		}
	}
	if len(links) != 0 {
		t.Fatalf("rolled-back links = %#v", links)
	}
	for _, item := range bundle.Items[:2] {
		id, err := deterministicLibraryImportID("remote", storage.DefaultProjectID, item)
		if err != nil {
			t.Fatal(err)
		}
		if normalizeLibraryKind(item.Kind) == "script" {
			if _, err := baseStore.GetScript(id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("durable script %s survived rollback: %v", id, err)
			}
		} else if _, _, err := baseStore.GetScenario(id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("durable scenario %s survived rollback: %v", id, err)
		}
	}
}

type failSecondLibraryLinkStore struct {
	*libraryAtomicForwardingStore
	mu       sync.Mutex
	linkSave int
}

func (s *failSecondLibraryLinkStore) SaveSetting(key, value string) error {
	if key == librarySyncLinksSettingKey {
		s.mu.Lock()
		s.linkSave++
		fail := s.linkSave >= 2 && s.linkSave <= 4
		s.mu.Unlock()
		if fail {
			return errors.New("injected link persistence failure")
		}
	}
	return s.Store.SaveSetting(key, value)
}

func (s *failSecondLibraryLinkStore) CompareAndSwapSetting(key, expectedValue, nextValue string) (bool, error) {
	if key == librarySyncLinksSettingKey {
		s.mu.Lock()
		s.linkSave++
		fail := s.linkSave >= 2 && s.linkSave <= 4
		s.mu.Unlock()
		if fail {
			return false, errors.New("injected link persistence failure")
		}
	}
	return s.libraryAtomicForwardingStore.CompareAndSwapSetting(key, expectedValue, nextValue)
}

func TestLibraryBundleRollsBackWhenSynchronizationLinkCannotCommit(t *testing.T) {
	baseStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "bundle-link-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer baseStore.Close()
	store := &failSecondLibraryLinkStore{libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: baseStore}}
	server := NewServer(ServerConfig{Store: store})
	first := libraryTestScript("source-one", "Source One", "source-project", "/one")
	second := libraryTestScript("source-two", "Source Two", "source-project", "/two")
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source", ProjectID: "source-project"}, Items: []LibraryBundleItem{
		{Kind: "script", SourceInstanceID: "source", SourceItemID: first.ID, ProjectID: "source-project", Name: first.Name, Script: first, ContentHash: hashLibraryScript(first)},
		{Kind: "script", SourceInstanceID: "source", SourceItemID: second.ID, ProjectID: "source-project", Name: second.Name, Script: second, ContentHash: hashLibraryScript(second)},
	}}
	response, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bundle}, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Conflicts) != 1 || !strings.Contains(response.Conflicts[0].Reason, "synchronization link") {
		t.Fatalf("response = %#v", response)
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("scripts survived failed link commit: %#v", scripts)
	}
}

func TestLibraryResultEvidenceRaceKeepsOneRowAndMatchingLink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result-replicas.db")
	firstStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer firstStore.Close()
	secondStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()
	firstServer := NewServer(ServerConfig{Store: firstStore})
	secondServer := NewServer(ServerConfig{Store: secondStore})

	makeBundle := func(status string) LibraryBundle {
		testRun := &ManagedTest{ID: "source-result", Name: "Source Result", Status: status, ProjectID: "source-project", DurationStr: "1m", TotalVUsers: 1}
		summary := libraryResultSummary(testRun)
		item := LibraryBundleItem{Kind: "result", SourceInstanceID: "source", SourceItemID: testRun.ID, ProjectID: "source-project", Name: testRun.Name, Result: &LibraryResultPayload{Test: testRun, Summary: summary}, ContentHash: hashLibraryResultWithSummary(testRun, summary), ContentHashVersion: libraryResultHashVersionCurrent}
		return LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source", ProjectID: "source-project"}, Items: []LibraryBundleItem{item}}
	}
	bundles := []LibraryBundle{makeBundle("COMPLETE"), makeBundle("FAILED")}
	servers := []*Server{firstServer, secondServer}
	type outcome struct {
		response *LibraryImportResponse
		err      error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := range servers {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			response, err := servers[index].importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bundles[index]}, "remote")
			outcomes <- outcome{response: response, err: err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(outcomes)

	successes := 0
	conflicts := 0
	for got := range outcomes {
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.response.Conflicts) == 0 && len(got.response.Items) == 1 {
			successes++
		} else if len(got.response.Conflicts) == 1 {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes/conflicts = %d/%d, want 1/1", successes, conflicts)
	}
	imports, err := firstStore.ListExternalResultImports(storage.ExternalResultImportFilter{ProjectID: storage.DefaultProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 1 {
		t.Fatalf("imports = %#v", imports)
	}
	contentHash, _ := imports[0].RawResult["content_hash"].(string)
	rawLinks, err := firstStore.GetSetting(librarySyncLinksSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	var links []librarySyncLink
	if err := json.Unmarshal([]byte(rawLinks), &links); err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].RemoteContentHash != contentHash || links[0].LocalContentHash != contentHash || links[0].LocalItemID != imports[0].ID {
		t.Fatalf("row/link mismatch: import=%#v links=%#v", imports[0], links)
	}
}

type blockThenFailLibraryResultStore struct {
	*libraryAtomicForwardingStore
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockThenFailLibraryResultStore) CreateExternalResultImportIfAbsent(result *storage.ExternalResultImport) (bool, error) {
	if result != nil && strings.HasSuffix(result.Source, "/source-failure") {
		s.once.Do(func() { close(s.reached) })
		<-s.release
		return false, errors.New("injected final result failure")
	}
	return s.libraryAtomicForwardingStore.CreateExternalResultImportIfAbsent(result)
}

func TestLibraryImportClaimSerializesSameIdentityUntilFailedBundleRollsBack(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	host := NewServer(ServerConfig{})
	sourceScript := host.runner.StoreScript(libraryTestScript("source-script", "Source Script", "source-project", "/source"))
	testRun, err := host.runner.CreateTest(CreateTestRequest{
		Name: "Source Result", Duration: "1m", ProjectID: "source-project",
		Groups: []CreateGroupEntry{{Name: "source", ScriptID: sourceScript.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	attachLibraryArchiveFixture(t, host, testRun)
	resultItem, err := host.libraryResultBundleItem(testRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	archiveResponse := httptest.NewRecorder()
	host.mux.ServeHTTP(archiveResponse, httptest.NewRequest(http.MethodGet, "/api/v1/library/results/"+testRun.ID+"/archive", nil))
	if archiveResponse.Code != http.StatusOK {
		t.Fatalf("archive export = %d %s", archiveResponse.Code, archiveResponse.Body.String())
	}
	resultItem.SourceInstanceID = "source"
	resultItem.Result.ArchiveTransfer = ""
	resultItem.Result.ArchiveZipB64 = base64.StdEncoding.EncodeToString(archiveResponse.Body.Bytes())

	scenarioRequest := CreateScenarioRequest{
		Name: "Source Scenario", Duration: "1m",
		Groups: []CreateGroupEntry{{Name: "source", ScriptID: sourceScript.ID, VUsers: 1}},
	}
	scriptItem := LibraryBundleItem{Kind: "script", SourceInstanceID: "source", SourceItemID: sourceScript.ID, ProjectID: "source-project", Name: sourceScript.Name, Script: sourceScript.Script, ContentHash: hashLibraryScript(sourceScript.Script)}
	scenarioItem := LibraryBundleItem{Kind: "scenario", SourceInstanceID: "source", SourceItemID: "source-scenario", ProjectID: "source-project", Name: scenarioRequest.Name, Scenario: &LibraryScenarioPayload{ID: "source-scenario", Request: scenarioRequest}, ContentHash: hashCanonical(scenarioRequest), Dependencies: []LibraryDependency{{Kind: "script", ID: sourceScript.ID, ProjectID: "source-project", ContentHash: scriptItem.ContentHash}}}
	failureTest := &ManagedTest{ID: "source-failure", Name: "Failure Trigger", Status: "COMPLETE", ProjectID: "source-project", DurationStr: "1m", TotalVUsers: 1}
	failureSummary := libraryResultSummary(failureTest)
	failureItem := LibraryBundleItem{Kind: "result", SourceInstanceID: "source", SourceItemID: failureTest.ID, ProjectID: "source-project", Name: failureTest.Name, Result: &LibraryResultPayload{Test: failureTest, Summary: failureSummary}, ContentHash: hashLibraryResultWithSummary(failureTest, failureSummary), ContentHashVersion: libraryResultHashVersionCurrent}
	commonItems := []LibraryBundleItem{scriptItem, scenarioItem, resultItem}
	aBundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source", ProjectID: "source-project"}, Items: append(append([]LibraryBundleItem(nil), commonItems...), failureItem)}
	bBundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source", ProjectID: "source-project"}, Items: append([]LibraryBundleItem(nil), commonItems...)}

	path := filepath.Join(t.TempDir(), "claimed-by-peer.db")
	baseStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer baseStore.Close()
	peerStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer peerStore.Close()
	blockingStore := &blockThenFailLibraryResultStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: baseStore},
		reached:                      make(chan struct{}),
		release:                      make(chan struct{}),
	}
	aServer := NewServer(ServerConfig{Store: blockingStore})

	type importOutcome struct {
		response *LibraryImportResponse
		err      error
	}
	aDone := make(chan importOutcome, 1)
	go func() {
		response, err := aServer.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: aBundle}, "remote")
		aDone <- importOutcome{response: response, err: err}
	}()
	select {
	case <-blockingStore.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("first replica did not reach the injected final failure")
	}

	// Start the peer after A's durable writes so its initial HA cache represents
	// the exact state a replacement/controller replica would load.
	bServer := NewServer(ServerConfig{Store: peerStore})
	bResponse, err := bServer.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bBundle}, "remote")
	if err != nil || len(bResponse.Conflicts) != 1 || !strings.Contains(bResponse.Conflicts[0].Reason, "already being mutated") {
		t.Fatalf("in-flight peer import = %#v, %v", bResponse, err)
	}
	close(blockingStore.release)
	aOutcome := <-aDone
	if aOutcome.err != nil || aOutcome.response == nil || len(aOutcome.response.Conflicts) != 1 || !strings.Contains(aOutcome.response.Conflicts[0].Reason, "injected final result failure") {
		t.Fatalf("aborting replica = %#v, %v", aOutcome.response, aOutcome.err)
	}
	// A fresh peer cache now observes the completed compensation and can claim
	// the exact identities without racing a delete against its link commit.
	bServer = NewServer(ServerConfig{Store: peerStore})
	bResponse, err = bServer.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bBundle}, "remote")
	if err != nil || len(bResponse.Conflicts) != 0 || len(bResponse.Items) != len(commonItems) {
		t.Fatalf("peer retry after rollback = %#v, %v", bResponse, err)
	}

	localIDs := make(map[string]string, len(bResponse.Items))
	for _, item := range bResponse.Items {
		localIDs[item.Kind] = item.LocalItemID
	}
	if _, err := baseStore.GetScript(localIDs["script"]); err != nil {
		t.Fatalf("peer-owned script was removed: %v", err)
	}
	if _, _, err := baseStore.GetScenario(localIDs["scenario"]); err != nil {
		t.Fatalf("peer-owned scenario was removed: %v", err)
	}
	result, err := baseStore.GetExternalResultImport(localIDs["result"])
	if err != nil {
		t.Fatalf("peer-owned result was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(libraryImportedArchiveDir(result.ID), "manifest.json")); err != nil {
		t.Fatalf("peer-owned result archive was removed: %v", err)
	}
	failureID, err := deterministicLibraryImportID("remote", storage.DefaultProjectID, failureItem)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := baseStore.GetExternalResultImport(failureID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed final result unexpectedly persisted: %v", err)
	}
	rawLinks, err := baseStore.GetSetting(librarySyncLinksSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	var links []librarySyncLink
	if err := json.Unmarshal([]byte(rawLinks), &links); err != nil {
		t.Fatal(err)
	}
	if len(links) != len(commonItems) {
		t.Fatalf("peer-owned links = %#v", links)
	}
	for _, link := range links {
		if strings.TrimSpace(link.MutationID) == "" {
			t.Fatalf("peer-owned link has no mutation fence: %#v", link)
		}
	}
}

func TestExpiredLibraryImportClaimRecoversAssetBeforeLinkKillWindow(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "120ms")
	path := filepath.Join(t.TempDir(), "claim-restart.db")
	firstStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	source := libraryTestScript("source-script", "Source Script", "source-project", "/source")
	item := LibraryBundleItem{Kind: "script", SourceInstanceID: "source", SourceItemID: source.ID, ProjectID: "source-project", Name: source.Name, Script: source, ContentHash: hashLibraryScript(source)}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source", ProjectID: "source-project"}, Items: []LibraryBundleItem{item}}
	firstServer := NewServer(ServerConfig{Store: firstStore})
	claims, err := firstServer.acquireLibraryImportClaims(bundle, "remote", storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	localID, err := deterministicLibraryImportID("remote", storage.DefaultProjectID, item)
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.prepare(item, localID, nil, nil, nil, nil, false, false, ""); err != nil {
		t.Fatal(err)
	}
	created := cloneScriptForLibrary(source)
	created.ID = localID
	created.ProjectID = storage.DefaultProjectID
	created.RefKey = ""
	created.CreatedAt = time.Time{}
	created.UpdatedAt = time.Time{}
	if _, err := firstServer.runner.CreateScriptDurable(created); err != nil {
		t.Fatal(err)
	}
	// Simulate process death: the durable prepared intent remains, its heartbeat
	// stops, and no in-memory rollback or terminal marker runs.
	claims.stopHeartbeat()
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(180 * time.Millisecond)

	secondStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()
	secondServer := NewServer(ServerConfig{Store: secondStore})
	if err := secondServer.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatal(err)
	}
	if _, err := secondStore.GetScript(localID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("asset-before-link kill window survived restart recovery: %v", err)
	}
	remaining, err := secondServer.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("recovered claims = %#v", remaining)
	}
	response, err := secondServer.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bundle}, "remote")
	if err != nil || len(response.Conflicts) != 0 || len(response.Items) != 1 {
		t.Fatalf("retry after restart recovery = %#v, %v", response, err)
	}
}

func TestExpiredLibraryImportClaimRecoversResultRowArchiveAndLink(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "120ms")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	host := NewServer(ServerConfig{})
	sourceScript := host.runner.StoreScript(libraryTestScript("archive-source", "Archive Source", "source-project", "/archive"))
	testRun, err := host.runner.CreateTest(CreateTestRequest{
		Name: "Crash Archive", Duration: "1m", ProjectID: "source-project",
		Groups: []CreateGroupEntry{{Name: "archive", ScriptID: sourceScript.ID, VUsers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	attachLibraryArchiveFixture(t, host, testRun)
	item, err := host.libraryResultBundleItem(testRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	archiveResponse := httptest.NewRecorder()
	host.mux.ServeHTTP(archiveResponse, httptest.NewRequest(http.MethodGet, "/api/v1/library/results/"+testRun.ID+"/archive", nil))
	if archiveResponse.Code != http.StatusOK {
		t.Fatalf("archive export = %d %s", archiveResponse.Code, archiveResponse.Body.String())
	}
	item.SourceInstanceID = "source"
	item.Result.ArchiveTransfer = ""
	item.Result.ArchiveZipB64 = base64.StdEncoding.EncodeToString(archiveResponse.Body.Bytes())
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source", ProjectID: "source-project"}, Items: []LibraryBundleItem{item}}

	path := filepath.Join(t.TempDir(), "archive-claim-restart.db")
	firstStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	firstServer := NewServer(ServerConfig{Store: firstStore})
	claims, err := firstServer.acquireLibraryImportClaims(bundle, "remote", storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	item.importMutationID = claims.mutationID(item)
	localID, err := deterministicLibraryImportID("remote", storage.DefaultProjectID, item)
	if err != nil {
		t.Fatal(err)
	}
	archivePath := libraryImportedArchiveDir(localID)
	if err := claims.prepare(item, localID, nil, nil, nil, nil, true, false, archivePath); err != nil {
		t.Fatal(err)
	}
	importedID, _, activated, conflict := firstServer.importLibraryResultWithOutcome(item, storage.DefaultProjectID, "remote", nil)
	if conflict != nil || importedID != localID || !activated {
		t.Fatalf("pre-crash result import = %q, activated=%v, conflict=%+v", importedID, activated, conflict)
	}
	if _, err := os.Stat(filepath.Join(archivePath, "manifest.json")); err != nil {
		t.Fatalf("pre-crash archive missing: %v", err)
	}
	// Kill after row/archive/link activation but before the applied checkpoint.
	claims.stopHeartbeat()
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(180 * time.Millisecond)

	secondStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()
	secondServer := NewServer(ServerConfig{Store: secondStore})
	if err := secondServer.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatal(err)
	}
	if _, err := secondStore.GetExternalResultImport(localID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("result row survived expired-claim recovery: %v", err)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("result archive survived expired-claim recovery: %v", err)
	}
	rawLinks, err := secondStore.GetSetting(librarySyncLinksSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	var links []librarySyncLink
	if strings.TrimSpace(rawLinks) != "" {
		if err := json.Unmarshal([]byte(rawLinks), &links); err != nil {
			t.Fatal(err)
		}
	}
	if len(links) != 0 {
		t.Fatalf("result link survived expired-claim recovery: %#v", links)
	}
	response, err := secondServer.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bundle}, "remote")
	if err != nil || len(response.Conflicts) != 0 || len(response.Items) != 1 {
		t.Fatalf("archive retry after recovery = %#v, %v", response, err)
	}
	if _, err := os.Stat(filepath.Join(archivePath, "manifest.json")); err != nil {
		t.Fatalf("archive retry did not converge: %v", err)
	}
}

func TestLibraryImportClaimsAllowUnrelatedBundleWhilePeerIsInFlight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unrelated-claims.db")
	firstStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer firstStore.Close()
	secondStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer secondStore.Close()
	blockingStore := &blockThenFailLibraryResultStore{
		libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: firstStore},
		reached:                      make(chan struct{}),
		release:                      make(chan struct{}),
	}
	firstServer := NewServer(ServerConfig{Store: blockingStore})
	secondServer := NewServer(ServerConfig{Store: secondStore})

	firstScript := libraryTestScript("first-source", "First Source", "source-project", "/first")
	failureTest := &ManagedTest{ID: "source-failure", Name: "Failure", Status: "COMPLETE", ProjectID: "source-project", DurationStr: "1m", TotalVUsers: 1}
	failureSummary := libraryResultSummary(failureTest)
	firstBundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source-a", ProjectID: "source-project"}, Items: []LibraryBundleItem{
		{Kind: "script", SourceInstanceID: "source-a", SourceItemID: firstScript.ID, ProjectID: "source-project", Name: firstScript.Name, Script: firstScript, ContentHash: hashLibraryScript(firstScript)},
		{Kind: "result", SourceInstanceID: "source-a", SourceItemID: failureTest.ID, ProjectID: "source-project", Name: failureTest.Name, Result: &LibraryResultPayload{Test: failureTest, Summary: failureSummary}, ContentHash: hashLibraryResultWithSummary(failureTest, failureSummary), ContentHashVersion: libraryResultHashVersionCurrent},
	}}
	secondScript := libraryTestScript("second-source", "Second Source", "source-project", "/second")
	secondBundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source-b", ProjectID: "source-project"}, Items: []LibraryBundleItem{
		{Kind: "script", SourceInstanceID: "source-b", SourceItemID: secondScript.ID, ProjectID: "source-project", Name: secondScript.Name, Script: secondScript, ContentHash: hashLibraryScript(secondScript)},
	}}

	firstDone := make(chan error, 1)
	go func() {
		_, err := firstServer.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: firstBundle}, "remote")
		firstDone <- err
	}()
	select {
	case <-blockingStore.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("first bundle did not reach its injected failure")
	}
	secondResponse, err := secondServer.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: secondBundle}, "remote")
	if err != nil || len(secondResponse.Conflicts) != 0 || len(secondResponse.Items) != 1 {
		t.Fatalf("unrelated in-flight import = %#v, %v", secondResponse, err)
	}
	close(blockingStore.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if _, err := secondStore.GetScript(secondResponse.Items[0].LocalItemID); err != nil {
		t.Fatalf("unrelated import was removed by peer rollback: %v", err)
	}
}

func TestExpiredLibraryImportClaimRecoversWithAlreadyRunningStaleReplica(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "120ms")
	path := filepath.Join(t.TempDir(), "claim-stale-replica.db")
	firstStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		firstStore.Close()
		t.Fatal(err)
	}
	defer secondStore.Close()
	firstServer := NewServer(ServerConfig{Store: firstStore})
	// Construct the replacement before the crash mutation so its initial
	// in-memory script view is deliberately stale.
	secondServer := NewServer(ServerConfig{Store: secondStore})

	source := libraryTestScript("stale-source", "Stale Source", "source-project", "/stale")
	item := LibraryBundleItem{Kind: "script", SourceInstanceID: "source", SourceItemID: source.ID, ProjectID: "source-project", Name: source.Name, Script: source, ContentHash: hashLibraryScript(source)}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source", ProjectID: "source-project"}, Items: []LibraryBundleItem{item}}
	claims, err := firstServer.acquireLibraryImportClaims(bundle, "remote", storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	localID, err := deterministicLibraryImportID("remote", storage.DefaultProjectID, item)
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.prepare(item, localID, nil, nil, nil, nil, false, false, ""); err != nil {
		t.Fatal(err)
	}
	created := cloneScriptForLibrary(source)
	created.ID = localID
	created.ProjectID = storage.DefaultProjectID
	created.RefKey = ""
	created.CreatedAt = time.Time{}
	created.UpdatedAt = time.Time{}
	if _, err := firstServer.runner.CreateScriptDurable(created); err != nil {
		t.Fatal(err)
	}
	if _, exists := secondServer.runner.GetScript(localID); exists {
		t.Fatal("replacement replica unexpectedly observed the crash asset before recovery")
	}
	claims.stopHeartbeat()
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(180 * time.Millisecond)
	if err := secondServer.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatal(err)
	}
	if _, err := secondStore.GetScript(localID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale replacement left crash-created asset behind: %v", err)
	}
}

func TestFailedBundleRollbackRetainsClaimForReplicaRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollback-recovery.db")
	baseStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer baseStore.Close()
	failingStore := &failResultAndRollbackDeleteStore{libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: baseStore}}
	server := NewServer(ServerConfig{Store: failingStore})

	script := libraryTestScript("rollback-source", "Rollback Source", "source-project", "/rollback")
	testRun := &ManagedTest{ID: "rollback-result", Name: "Rollback Result", Status: "COMPLETE", ProjectID: "source-project", DurationStr: "1m", TotalVUsers: 1}
	summary := libraryResultSummary(testRun)
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Source: LibraryBundleSource{InstanceID: "source", ProjectID: "source-project"}, Items: []LibraryBundleItem{
		{Kind: "script", SourceInstanceID: "source", SourceItemID: script.ID, ProjectID: "source-project", Name: script.Name, Script: script, ContentHash: hashLibraryScript(script)},
		{Kind: "result", SourceInstanceID: "source", SourceItemID: testRun.ID, ProjectID: "source-project", Name: testRun.Name, Result: &LibraryResultPayload{Test: testRun, Summary: summary}, ContentHash: hashLibraryResultWithSummary(testRun, summary), ContentHashVersion: libraryResultHashVersionCurrent},
	}}
	if _, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bundle}, "remote"); err == nil {
		t.Fatal("bundle import unexpectedly hid its rollback failure")
	}
	localID, err := deterministicLibraryImportID("remote", storage.DefaultProjectID, bundle.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := baseStore.GetScript(localID); err != nil {
		t.Fatalf("injected rollback did not leave the recovery fixture: %v", err)
	}
	claims, err := server.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) == 0 || claims[0].Committed {
		t.Fatalf("failed rollback recovery evidence was discarded: %#v", claims)
	}

	replacement := NewServer(ServerConfig{Store: baseStore})
	if err := replacement.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatal(err)
	}
	if _, err := baseStore.GetScript(localID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replacement did not finish the retained rollback: %v", err)
	}
	remaining, err := replacement.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("recovered rollback claims = %#v", remaining)
	}
}

func TestLibraryImportCompletionFailsAfterOwnerFenceLoss(t *testing.T) {
	server := NewServer(ServerConfig{})
	source := libraryTestScript("fenced-source", "Fenced Source", "source-project", "/fenced")
	item := LibraryBundleItem{Kind: "script", SourceInstanceID: "source", SourceItemID: source.ID, ProjectID: "source-project", Script: source, ContentHash: hashLibraryScript(source)}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}
	claims, err := server.acquireLibraryImportClaims(bundle, "remote", storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	claims.stopHeartbeat()
	if err := server.mutateLibraryImportClaims(func(existing *[]libraryImportClaim) (bool, error) {
		for index := range *existing {
			if (*existing)[index].OwnerID == claims.owner {
				(*existing)[index].OwnerID = "replacement-owner"
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := claims.complete(); err == nil || !strings.Contains(err.Error(), "lost its owner fence") {
		t.Fatalf("completion after owner loss = %v", err)
	}
	if err := server.removeLibraryImportClaims("replacement-owner"); err != nil {
		t.Fatal(err)
	}
}

func TestExternalResultImportCloneDeepCopiesEvidence(t *testing.T) {
	nested := map[string]any{"value": "before"}
	sliceEntry := map[string]any{"value": "before"}
	original := &storage.ExternalResultImport{
		ID: "evidence", Summary: map[string]any{"nested": nested},
		RawResult: map[string]any{"items": []any{sliceEntry}},
	}
	cloned := cloneExternalResultImport(original)
	nested["value"] = "after"
	sliceEntry["value"] = "after"
	if got := cloned.Summary["nested"].(map[string]any)["value"]; got != "before" {
		t.Fatalf("cloned summary changed through caller alias: %#v", got)
	}
	if got := cloned.RawResult["items"].([]any)[0].(map[string]any)["value"]; got != "before" {
		t.Fatalf("cloned raw evidence changed through caller alias: %#v", got)
	}
}

func TestExpiredClaimsRecoverScenarioBeforeRemappedScriptDependency(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "120ms")
	path := filepath.Join(t.TempDir(), "scenario-order-recovery.db")
	firstStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := storage.NewSQLiteStore(path)
	if err != nil {
		firstStore.Close()
		t.Fatal(err)
	}
	defer secondStore.Close()
	sourceScript := libraryTestScript("source-script", "Source Script", "source-project", "/ordered")
	scriptItem := LibraryBundleItem{Kind: "script", SourceInstanceID: "source", SourceItemID: sourceScript.ID, ProjectID: "source-project", Name: sourceScript.Name, Script: sourceScript, ContentHash: hashLibraryScript(sourceScript)}
	sourceRequest := CreateScenarioRequest{Name: "Source Scenario", Duration: "1m", Groups: []CreateGroupEntry{{Name: "load", ScriptID: sourceScript.ID, VUsers: 1}}}
	scenarioItem := LibraryBundleItem{Kind: "scenario", SourceInstanceID: "source", SourceItemID: "source-scenario", ProjectID: "source-project", Name: sourceRequest.Name, Scenario: &LibraryScenarioPayload{ID: "source-scenario", Request: sourceRequest}, ContentHash: hashCanonical(sourceRequest)}
	// Put the dependent scenario first to verify durable sequencing follows the
	// actual import phases (scripts, then scenarios), not attacker-controlled
	// bundle order.
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{scenarioItem, scriptItem}}
	firstServer := newLibraryAtomicTestServerAfterStartupRecovery(t, firstStore)
	replacement := newLibraryAtomicTestServerAfterStartupRecovery(t, secondStore)
	claims, err := firstServer.acquireLibraryImportClaims(bundle, "remote", storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	scriptClaimKey := claims.key(scriptItem)
	scenarioClaimKey := claims.key(scenarioItem)
	claims.mu.RLock()
	scriptSequence := claims.claims[scriptClaimKey].Sequence
	scenarioSequence := claims.claims[scenarioClaimKey].Sequence
	claims.mu.RUnlock()
	if scriptSequence >= scenarioSequence {
		t.Fatalf("durable compensation order script/scenario = %d/%d", scriptSequence, scenarioSequence)
	}

	scriptID, err := deterministicLibraryImportID("remote", storage.DefaultProjectID, scriptItem)
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.prepare(scriptItem, scriptID, nil, nil, nil, nil, false, false, ""); err != nil {
		t.Fatal(err)
	}
	createdScript := cloneScriptForLibrary(sourceScript)
	createdScript.ID = scriptID
	createdScript.ProjectID = storage.DefaultProjectID
	createdScript.RefKey = ""
	createdScript.CreatedAt = time.Time{}
	createdScript.UpdatedAt = time.Time{}
	if _, err := firstServer.runner.CreateScriptDurable(createdScript); err != nil {
		t.Fatal(err)
	}
	if err := claims.markApplied(scriptItem, false); err != nil {
		t.Fatal(err)
	}

	scenarioID, err := deterministicLibraryImportID("remote", storage.DefaultProjectID, scenarioItem)
	if err != nil {
		t.Fatal(err)
	}
	localRequest := cloneScenarioRequestForLibrary(sourceRequest)
	localRequest.Groups[0].ScriptID = scriptID
	intentHash := firstServer.libraryScenarioRequestRecoveryHash(localRequest)
	if err := claims.prepare(scenarioItem, scenarioID, nil, nil, nil, nil, false, false, "", intentHash); err != nil {
		t.Fatal(err)
	}
	if _, err := firstServer.runner.StoreScenarioWithIDCreateOnly(scenarioID, localRequest); err != nil {
		t.Fatal(err)
	}
	// Crash in the scenario asset-before-link window.
	claims.stopHeartbeat()
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(180 * time.Millisecond)

	if err := replacement.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := secondStore.GetScenario(scenarioID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("remapped scenario survived recovery: %v", err)
	}
	if _, err := secondStore.GetScript(scriptID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("scenario dependency survived recovery: %v", err)
	}
}

func TestDuplicateLibraryIdentityIsRejectedBeforeMutation(t *testing.T) {
	server := NewServer(ServerConfig{})
	source := libraryTestScript("duplicate-source", "Duplicate Source", "source-project", "/duplicate")
	item := LibraryBundleItem{Kind: "script", SourceInstanceID: "source", SourceItemID: source.ID, ProjectID: "source-project", Name: source.Name, Script: source, ContentHash: hashLibraryScript(source)}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item, item}}
	if _, err := server.acquireLibraryImportClaims(bundle, "remote", storage.DefaultProjectID); err == nil || !strings.Contains(err.Error(), "duplicate library import identity") {
		t.Fatalf("duplicate identity import = %v", err)
	}
	if scripts := server.runner.ListScripts(); len(scripts) != 0 {
		t.Fatalf("duplicate identity mutated scripts: %#v", scripts)
	}
	claims, err := server.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("duplicate identity retained claims: %#v", claims)
	}
}

func TestIdenticalLibraryResultRetryIgnoresPriorInternalMutationMarker(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "result-marker-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	testRun := &ManagedTest{ID: "source-result", Name: "Source Result", Status: "COMPLETE", ProjectID: "source-project", DurationStr: "1m", TotalVUsers: 1}
	summary := libraryResultSummary(testRun)
	item := LibraryBundleItem{Kind: "result", SourceInstanceID: "source", SourceItemID: testRun.ID, ProjectID: "source-project", Name: testRun.Name, Result: &LibraryResultPayload{Test: testRun, Summary: summary}, ContentHash: hashLibraryResultWithSummary(testRun, summary), ContentHashVersion: libraryResultHashVersionCurrent}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}
	first, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bundle}, "remote")
	if err != nil || len(first.Conflicts) != 0 || len(first.Items) != 1 {
		t.Fatalf("first result import = %#v, %v", first, err)
	}
	second, err := server.importLibraryBundle(LibraryImportRequest{ProjectID: storage.DefaultProjectID, Bundle: bundle}, "remote")
	if err != nil || len(second.Conflicts) != 0 || len(second.Items) != 1 || second.Items[0].Action != "unchanged" {
		t.Fatalf("identical result retry = %#v, %v", second, err)
	}
	stored, err := store.GetExternalResultImport(first.Items[0].LocalItemID)
	if err != nil {
		t.Fatal(err)
	}
	if marker, _ := stored.RawResult[libraryResultMutationIDKey].(string); strings.TrimSpace(marker) == "" {
		t.Fatalf("stored result lacks durable mutation ownership marker: %#v", stored.RawResult)
	}
}

func TestExpiredResultClaimPreservesPeerEvidenceWithSameContentHash(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "120ms")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "result-peer-marker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	testRun := &ManagedTest{ID: "source-result", Name: "Source Result", Status: "COMPLETE", ProjectID: "source-project", DurationStr: "1m", TotalVUsers: 1}
	summary := libraryResultSummary(testRun)
	item := LibraryBundleItem{Kind: "result", SourceInstanceID: "source", SourceItemID: testRun.ID, ProjectID: "source-project", Name: testRun.Name, Result: &LibraryResultPayload{Test: testRun, Summary: summary}, ContentHash: hashLibraryResultWithSummary(testRun, summary), ContentHashVersion: libraryResultHashVersionCurrent}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}
	claims, err := server.acquireLibraryImportClaims(bundle, "remote", storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	localID, err := deterministicLibraryImportID("remote", storage.DefaultProjectID, item)
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.prepare(item, localID, nil, nil, nil, nil, false, false, ""); err != nil {
		t.Fatal(err)
	}
	peer := &storage.ExternalResultImport{
		ID: localID, Provider: "apexload", Name: "Peer Evidence", ProjectID: storage.DefaultProjectID,
		Source: "peer", Summary: map[string]any{"provider": "apexload"},
		RawResult: map[string]any{"content_hash": item.ContentHash, libraryResultMutationIDKey: "peer-owner"}, CreatedAt: time.Now().UTC(),
	}
	if created, err := store.CreateExternalResultImportIfAbsent(peer); err != nil || !created {
		t.Fatalf("create peer evidence = %v, %v", created, err)
	}
	claims.stopHeartbeat()
	time.Sleep(180 * time.Millisecond)
	if err := server.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetExternalResultImport(localID)
	if err != nil {
		t.Fatalf("peer evidence was deleted: %v", err)
	}
	if marker, _ := stored.RawResult[libraryResultMutationIDKey].(string); marker != "peer-owner" {
		t.Fatalf("peer evidence changed: %#v", stored.RawResult)
	}
}

func TestDurableLibraryImportClaimMasksRollbackSecrets(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "claim-secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	prior := libraryTestScript("local-secret", "Local Secret", storage.DefaultProjectID, "/secret")
	prior.Config.HTTP.Auth.Password = "claim-must-not-persist-this-password"
	stored, err := server.runner.CreateScriptDurable(prior)
	if err != nil {
		t.Fatal(err)
	}
	remote := libraryTestScript("remote-secret", "Remote Secret", "source-project", "/remote")
	item := LibraryBundleItem{Kind: "script", SourceInstanceID: "source", SourceItemID: remote.ID, ProjectID: "source-project", Script: remote, ContentHash: hashLibraryScript(remote)}
	bundle := LibraryBundle{SchemaVersion: libraryBundleSchemaVersion, Items: []LibraryBundleItem{item}}
	claims, err := server.acquireLibraryImportClaims(bundle, "remote", storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := claims.prepare(item, stored.ID, nil, stored, nil, nil, false, false, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := store.GetSetting(libraryImportClaimsSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "claim-must-not-persist-this-password") || !strings.Contains(raw, maskedScriptSecret) {
		t.Fatalf("durable rollback claim was not projected: %s", raw)
	}
	if err := claims.complete(); err != nil {
		t.Fatal(err)
	}
}

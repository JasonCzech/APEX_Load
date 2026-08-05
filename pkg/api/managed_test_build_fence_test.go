package api

import (
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type managedTestCreateResult struct {
	test *ManagedTest
	err  error
}

func TestManagedTestCapacitySnapshotPreservesBuildFingerprint(t *testing.T) {
	createdAt := time.Date(2026, time.August, 5, 10, 11, 12, 13, time.UTC)
	scheduledAt := createdAt.Add(time.Minute)
	pendingSince := createdAt.Add(2 * time.Minute)
	startedAt := createdAt.Add(3 * time.Minute)
	pausedAt := createdAt.Add(4 * time.Minute)
	windowStart := createdAt.Add(5 * time.Minute)
	windowEnd := createdAt.Add(65 * time.Minute)
	timeslotID := "timeslot-fingerprint"
	goalMaxVUsers := 27
	original := &ManagedTest{
		ID: "fingerprint-test", RefKey: "LOC-RUN-20260805-0042", MaterializationKey: "plan:entry:occurrence",
		Status: "PAUSED", ProjectID: "project-fingerprint", TotalVUsers: 17,
		CapacityAdvisory: &CapacityAdvisory{RecommendedGenerators: 3},
		Assignment: &GeneratorAssignmentSettings{
			Mode: "weighted_regions", SelectedTargets: []string{"generator-a", "generator-b"},
			RegionWeights: map[string]int{"east": 60, "west": 40}, LoadZoneWeights: map[string]int{"private": 100},
			ShardStrategy: "round_robin", RequiredLabels: map[string]string{"tier": "performance"},
			TolerateGeneratorLoss: true, PoolID: "pool-a",
		},
		ResourceBaseline: true, StartDelay: "7s", Duration: 91 * time.Second, CreatedAt: createdAt,
		ScheduledStartAt: &scheduledAt, PendingSince: &pendingSince, StartedAt: &startedAt, PausedAt: &pausedAt,
		CumulativePause: 11 * time.Second, CapacityWindowStartAt: &windowStart, CapacityWindowEndAt: &windowEnd,
		TimeslotID: &timeslotID, GoalConfig: &GoalConfig{MaxVUsers: &goalMaxVUsers},
		ExecutionPlanApproval: &RunArchiveExecutionPlanApprovalSnapshot{
			ID: "approval-a", PlanID: "plan-a", PlanContentHash: "sha256:plan", Status: "approved",
			Policy: "quota_burst", ApprovedPeakVUsers: 29,
		},
		Groups: []TestGroup{{
			Protocol: "java_vuser", VUsers: 17, RampDown: "13s",
			Assignment: &GeneratorAssignmentSettings{
				Mode: "specific_hosts", SelectedTargets: []string{"generator-a"}, RequiredLabels: map[string]string{"java": "true"},
			},
			Stages: []storage.LoadStage{{Type: "ramp", Duration: "31s", TargetVUsers: 17}},
			script: &dsl.Script{Protocol: "java_vuser", Actions: []dsl.Action{{
				Type: "java_vuser_run", Params: map[string]any{"method": "actions"},
			}}},
		}},
	}

	snapshot := cloneManagedTestCapacitySnapshotLocked(original)
	want := managedTestBuildTestFingerprint(original)
	got := managedTestBuildTestFingerprint(snapshot)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capacity snapshot changed build fingerprint:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestCreateTestRetriesUnrelatedAdmissionChurn(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "retry-churn-script", "project-a")
	poolStore := &blockingLoadModelDataPoolStore{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-poolStore.release:
		default:
			close(poolStore.release)
		}
	})
	runner.dataPoolStore = poolStore
	runner.mu.Lock()
	runner.tests["unrelated-peer"] = &ManagedTest{
		ID: "unrelated-peer", Status: "QUEUED", ProjectID: "project-b", TotalVUsers: 1,
		Groups: []TestGroup{{Protocol: "http", VUsers: 1}},
	}
	runner.mu.Unlock()

	result := make(chan managedTestCreateResult, 1)
	go func() {
		created, err := runner.CreateTest(CreateTestRequest{
			Name: "retry unrelated churn", ProjectID: "project-a", Duration: "1m",
			Groups:    []CreateGroupEntry{{ScriptID: "retry-churn-script", VUsers: 1}},
			DataPools: []storage.ScenarioDataPoolBinding{{PoolID: "customers", Alias: "customers"}},
		})
		result <- managedTestCreateResult{test: created, err: err}
	}()

	select {
	case <-poolStore.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not reach the off-lock data-pool read")
	}
	runner.mu.Lock()
	runner.tests["unrelated-peer"].TotalVUsers = 2
	runner.tests["unrelated-peer"].Groups[0].VUsers = 2
	runner.mu.Unlock()
	close(poolStore.release)

	select {
	case got := <-result:
		if got.err != nil || got.test == nil {
			t.Fatalf("CreateTest after unrelated admission churn = (%#v, %v), want success", got.test, got.err)
		}
		if installed, ok := runner.GetTest(got.test.ID); !ok || installed.RefKey != got.test.RefKey {
			t.Fatalf("retried test was not installed exactly: installed=%#v ok=%t created=%#v", installed, ok, got.test)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not complete after unrelated admission churn")
	}
}

type blockingRunSequenceSettingsStore struct {
	mu        sync.Mutex
	values    map[string]string
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
	saveCalls int
}

func (s *blockingRunSequenceSettingsStore) GetSetting(key string) (string, error) {
	s.once.Do(func() {
		close(s.entered)
		<-s.release
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key], nil
}

func (s *blockingRunSequenceSettingsStore) SaveSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	s.saveCalls++
	return nil
}

func (s *blockingRunSequenceSettingsStore) saves() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveCalls
}

func TestCreateTestReusesRunReferenceAcrossSecondFenceRetry(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	storePreflightScript(runner, "retry-reference-script", storage.DefaultProjectID)
	settingsStore := &blockingRunSequenceSettingsStore{
		values: make(map[string]string), entered: make(chan struct{}), release: make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-settingsStore.release:
		default:
			close(settingsStore.release)
		}
	})
	runner.settingsStore = settingsStore
	runner.runRefSeqAllocator = newRunReferenceSequenceAllocator(runner.envCode, settingsStore)
	runner.mu.Lock()
	runner.tests["sequence-peer"] = &ManagedTest{
		ID: "sequence-peer", Status: "QUEUED", ProjectID: storage.DefaultProjectID, TotalVUsers: 1,
		Groups: []TestGroup{{Protocol: "http", VUsers: 1}},
	}
	runner.mu.Unlock()

	result := make(chan managedTestCreateResult, 1)
	go func() {
		created, err := runner.CreateTest(CreateTestRequest{
			Name: "retry one reference", Duration: "1m", ScriptID: "retry-reference-script", VUsers: 1,
		})
		result <- managedTestCreateResult{test: created, err: err}
	}()

	select {
	case <-settingsStore.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not reach run-reference allocation")
	}
	runner.mu.Lock()
	runner.tests["sequence-peer"].TotalVUsers = 2
	runner.tests["sequence-peer"].Groups[0].VUsers = 2
	runner.mu.Unlock()
	close(settingsStore.release)

	select {
	case got := <-result:
		if got.err != nil || got.test == nil {
			t.Fatalf("CreateTest after second-fence churn = (%#v, %v), want success", got.test, got.err)
		}
		if got.test.RefKey == "" {
			t.Fatal("retried test has no run reference")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not complete after second-fence churn")
	}
	if got := settingsStore.saves(); got != 1 {
		t.Fatalf("run-reference sequence saves = %d, want one allocation reused across retry", got)
	}
}

func TestManagedTestBuildFenceDetectsRuntimeCapacityMutation(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.mu.Lock()
	runner.tests["capacity-peer"] = &ManagedTest{
		ID: "capacity-peer", Status: "QUEUED", ProjectID: storage.DefaultProjectID, TotalVUsers: 1,
	}
	_, _, fence := runner.managedTestBuildSnapshotLocked(CreateTestRequest{})
	contentRevision := runner.contentRevision
	runner.tests["capacity-peer"].Status = "RUNNING"
	runner.tests["capacity-peer"].TotalVUsers = 250
	if runner.contentRevision != contentRevision {
		t.Fatal("runtime mutation unexpectedly changed the authoring content revision")
	}
	if runner.managedTestBuildFenceCurrentLocked(fence) {
		t.Fatal("build fence remained current after runtime capacity state changed")
	}
	runner.mu.Unlock()
}

func TestManagedTestBuildFenceDetectsSameTotalGroupRedistribution(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.mu.Lock()
	runner.tests["group-capacity-peer"] = &ManagedTest{
		ID: "group-capacity-peer", Status: "RUNNING", TotalVUsers: 10,
		Groups: []TestGroup{
			{Protocol: "http", VUsers: 4},
			{Protocol: "java_vuser", VUsers: 6},
		},
	}
	_, _, fence := runner.managedTestBuildSnapshotLocked(CreateTestRequest{})
	runner.tests["group-capacity-peer"].Groups[0].VUsers = 5
	runner.tests["group-capacity-peer"].Groups[1].VUsers = 5
	if runner.managedTestBuildFenceCurrentLocked(fence) {
		runner.mu.Unlock()
		t.Fatal("same-total group capacity redistribution did not invalidate build")
	}
	runner.mu.Unlock()
}

func TestManagedTestBuildFenceIgnoresUnrelatedTerminalHistoryMutation(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.mu.Lock()
	runner.tests["terminal-history"] = &ManagedTest{
		ID: "terminal-history", Status: "COMPLETE", ProjectID: storage.DefaultProjectID,
		TotalVUsers: 1, Groups: []TestGroup{{Protocol: "http", VUsers: 1}},
	}
	snapshot, _, fence := runner.managedTestBuildSnapshotLocked(CreateTestRequest{})
	if snapshot.tests["terminal-history"] != nil {
		runner.mu.Unlock()
		t.Fatal("request-scoped build retained unrelated terminal history")
	}
	runner.tests["terminal-history"].TotalVUsers = 10000
	runner.tests["terminal-history"].Groups[0].Protocol = "java_vuser"
	if !runner.managedTestBuildFenceCurrentLocked(fence) {
		runner.mu.Unlock()
		t.Fatal("unrelated terminal-only metadata invalidated admission build")
	}
	runner.mu.Unlock()
}

func TestManagedTestBuildFenceRetainsRequestedTerminalIdentity(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	const requestedID = "requested-terminal"
	runner.mu.Lock()
	runner.tests[requestedID] = &ManagedTest{
		ID: requestedID, RefKey: "LOC-RUN-20260805-0001", MaterializationKey: "first-owner",
		Status: "COMPLETE", ProjectID: storage.DefaultProjectID,
	}
	snapshot, _, fence := runner.managedTestBuildSnapshotLocked(CreateTestRequest{RequestedTestID: requestedID})
	if snapshot.tests[requestedID] == nil {
		runner.mu.Unlock()
		t.Fatal("requested terminal identity was omitted from request-scoped snapshot")
	}
	runner.tests[requestedID].MaterializationKey = "second-owner"
	if runner.managedTestBuildFenceCurrentLocked(fence) {
		runner.mu.Unlock()
		t.Fatal("requested terminal identity ownership change did not invalidate build")
	}
	runner.mu.Unlock()
}

func TestManagedTestBuildFenceDetectsLiveInventoryMutation(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	initialAt := time.Now().UTC()
	runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{
		Target: "generator-a:9090", GeneratorID: "generator-a", Status: "connected", DrainState: "active", AvailableCapacity: 100,
	}}, initialAt)

	runner.mu.RLock()
	_, _, fence := runner.managedTestBuildSnapshotLocked(CreateTestRequest{})
	contentRevision := runner.contentRevision
	runner.mu.RUnlock()
	runner.setCachedGeneratorInventory([]GeneratorInventoryEntry{{
		Target: "generator-a:9090", GeneratorID: "generator-a", Status: "connected", DrainState: "active", AvailableCapacity: 10,
	}}, initialAt.Add(time.Second))

	runner.mu.RLock()
	if runner.contentRevision != contentRevision {
		t.Fatal("inventory mutation unexpectedly changed the authoring content revision")
	}
	if runner.managedTestBuildFenceCurrentLocked(fence) {
		t.Fatal("build fence remained current after live inventory changed")
	}
	runner.mu.RUnlock()
}

func TestManagedTestBuildFenceDetectsSettingsMutation(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.mu.Lock()
	_, _, fence := runner.managedTestBuildSnapshotLocked(CreateTestRequest{})
	contentRevision := runner.contentRevision
	runner.settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators++
	if runner.contentRevision != contentRevision {
		t.Fatal("settings mutation unexpectedly changed the authoring content revision")
	}
	if runner.managedTestBuildFenceCurrentLocked(fence) {
		t.Fatal("build fence remained current after capacity settings changed")
	}
	runner.mu.Unlock()
}

func TestManagedTestBuildFenceDetectsProjectDeletionTombstone(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	runner.mu.Lock()
	_, _, fence := runner.managedTestBuildSnapshotLocked(CreateTestRequest{ProjectID: "project-delete-race"})
	contentRevision := runner.contentRevision
	runner.deletingProjects[normalizeProjectID("project-delete-race")] = struct{}{}
	if runner.contentRevision != contentRevision {
		t.Fatal("project deletion tombstone unexpectedly changed the authoring content revision")
	}
	if runner.managedTestBuildFenceCurrentLocked(fence) {
		t.Fatal("build fence remained current after a project deletion began")
	}
	runner.mu.Unlock()
}

func TestCreateTestInstallsCommittedSnapshotAfterAdmissionStateChanges(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	runner.mu.Lock()
	runner.tests["capacity-peer"] = &ManagedTest{
		ID: "capacity-peer", Status: "QUEUED", ProjectID: storage.DefaultProjectID, TotalVUsers: 1,
	}
	contentRevision := runner.contentRevision
	runner.mu.Unlock()

	store.blockTestCreate.Store(true)
	type createResult struct {
		test *ManagedTest
		err  error
	}
	result := make(chan createResult, 1)
	go func() {
		test, err := runner.CreateTest(CreateTestRequest{
			Name: "post-commit fence", Duration: "1m", ScriptID: "storage-evidence-script", VUsers: 1,
		})
		result <- createResult{test: test, err: err}
	}()

	select {
	case <-store.testCreateEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not reach its durable commit boundary")
	}
	runner.mu.Lock()
	runner.tests["capacity-peer"].Status = "RUNNING"
	runner.tests["capacity-peer"].TotalVUsers = 500
	if runner.contentRevision != contentRevision {
		t.Fatal("runtime capacity mutation unexpectedly changed contentRevision")
	}
	runner.mu.Unlock()
	close(store.releaseTestCreate)

	select {
	case created := <-result:
		if created.err != nil {
			t.Fatalf("CreateTest returned an error after its durable commit: %v", created.err)
		}
		if created.test == nil {
			t.Fatal("CreateTest returned no committed test")
		}
		installed, ok := runner.GetTest(created.test.ID)
		if !ok || installed.RefKey != created.test.RefKey {
			t.Fatalf("committed test was not installed exactly: installed=%#v ok=%t created=%#v", installed, ok, created.test)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not finish after durable commit")
	}
}

func TestCreateTestRejectsProjectQuotaRevisionChangedAtDurableCommit(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	const (
		projectID = "project-quota-create-race"
		scriptID  = "project-quota-create-race-script"
		testID    = "ba24c764-f708-4db1-a95c-f2cdcf98558c"
	)
	now := time.Now().UTC()
	if err := store.Store.SaveProject(&storage.Project{ID: projectID, Name: "Project quota create race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	storePreflightScript(runner, scriptID, projectID)

	store.blockTestCreate.Store(true)
	result := make(chan error, 1)
	go func() {
		_, err := runner.CreateTest(CreateTestRequest{
			RequestedTestID: testID,
			Name:            "project quota create race",
			ProjectID:       projectID,
			Duration:        "1m",
			ScriptID:        scriptID,
			VUsers:          2,
		})
		result <- err
	}()

	select {
	case <-store.testCreateEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not reach its durable commit boundary")
	}
	project, err := store.Store.GetProject(projectID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	project.Settings.MaxVUsersPerTest = 1
	if err := store.Store.SaveProject(project); err != nil {
		t.Fatalf("update project quota: %v", err)
	}
	close(store.releaseTestCreate)

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "project \""+projectID+"\" changed while the run was being prepared") {
			t.Fatalf("CreateTest error = %v, want changed-project evidence rejection", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not finish after the project quota update")
	}
	if durable, err := store.Store.GetTest(testID); err == nil {
		t.Fatalf("stale project-quota create committed durable test: %#v", durable)
	}
}

func TestCreateTestRejectsReleaseArchivedAtDurableCommit(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	const (
		projectID     = "release-create-race-project"
		applicationID = "release-create-race-application"
		releaseID     = "release-create-race-release"
		scriptID      = "release-create-race-script"
		testID        = "8a682afe-76cb-443c-b442-9b3e262f39f8"
	)
	now := time.Now().UTC()
	if err := store.Store.SaveProject(&storage.Project{ID: projectID, Name: "Release create race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if err := store.Store.SaveApplication(&storage.Application{ID: applicationID, ProjectID: projectID, Name: "Release create race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveApplication: %v", err)
	}
	if err := store.Store.SaveRelease(&storage.Release{ID: releaseID, ApplicationID: applicationID, ProjectID: projectID, Name: "Release create race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveRelease: %v", err)
	}
	storePreflightScript(runner, scriptID, projectID)

	store.blockTestCreate.Store(true)
	result := make(chan error, 1)
	go func() {
		_, err := runner.CreateTest(CreateTestRequest{
			RequestedTestID: testID,
			Name:            "release archive create race",
			ProjectID:       projectID,
			ApplicationID:   applicationID,
			ReleaseID:       releaseID,
			Duration:        "1m",
			ScriptID:        scriptID,
			VUsers:          1,
		})
		result <- err
	}()

	select {
	case <-store.testCreateEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not reach its durable commit boundary")
	}
	if err := store.Store.ArchiveRelease(releaseID); err != nil {
		t.Fatalf("ArchiveRelease: %v", err)
	}
	close(store.releaseTestCreate)

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "release is archived: "+releaseID) {
			t.Fatalf("CreateTest error = %v, want archived-release rejection", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not finish after release archival")
	}
	if durable, err := store.Store.GetTest(testID); err == nil {
		t.Fatalf("archived-release create committed durable test: %#v", durable)
	}
}

func TestCreateCommitAndProjectDeletionCannotLeaveInMemoryGhost(t *testing.T) {
	runner, store := newAdmissionEvidenceRunner(t)
	const (
		projectID = "project-create-delete-race"
		scriptID  = "project-create-delete-race-script"
		testID    = "71980962-5a80-4226-940e-23a4d1fcc12f"
	)
	now := time.Now().UTC()
	if err := store.Store.SaveProject(&storage.Project{ID: projectID, Name: "Create delete race", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	storePreflightScript(runner, scriptID, projectID)
	store.blockTestCreateReturn.Store(true)

	createdResult := make(chan managedTestCreateResult, 1)
	go func() {
		created, err := runner.CreateTest(CreateTestRequest{
			RequestedTestID: testID, Name: "create delete race", ProjectID: projectID,
			Duration: "1m", ScriptID: scriptID, VUsers: 1,
		})
		createdResult <- managedTestCreateResult{test: created, err: err}
	}()
	select {
	case <-store.testCreateCommitted:
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not commit before its return barrier")
	}

	deletionAdmitted := make(chan error, 1)
	go func() { deletionAdmitted <- runner.beginProjectDeletion(projectID) }()
	select {
	case err := <-deletionAdmitted:
		t.Fatalf("project deletion crossed the committed-create install boundary: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(store.releaseTestCreateReturn)
	select {
	case result := <-createdResult:
		if result.err != nil || result.test == nil || result.test.ID != testID {
			t.Fatalf("CreateTest = (%#v, %v), want committed identity", result.test, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test creation did not finish after its commit barrier released")
	}
	select {
	case err := <-deletionAdmitted:
		if err != nil {
			t.Fatalf("beginProjectDeletion: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("project deletion did not proceed after create installed")
	}
	if err := store.Store.DeleteProject(projectID); err != nil {
		runner.endProjectDeletion(projectID)
		t.Fatalf("DeleteProject: %v", err)
	}
	runner.forgetProject(projectID)
	runner.endProjectDeletion(projectID)
	if ghost, ok := runner.GetTest(testID); ok {
		t.Fatalf("deleted project retained in-memory test ghost: %#v", ghost)
	}
	if durable, err := store.Store.GetTest(testID); err == nil {
		t.Fatalf("deleted project retained durable test: %#v", durable)
	}
}

func TestCreateReturnsCommittedIdentityButSkipsInstallAfterLeaseLoss(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "lease-loss-create.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &admissionEvidenceStore{
		Store: base, reservationEntered: make(chan struct{}), releaseReservation: make(chan struct{}),
		testCreateEntered: make(chan struct{}), releaseTestCreate: make(chan struct{}),
		testCreateCommitted: make(chan struct{}), releaseTestCreateReturn: make(chan struct{}),
	}
	leadership := newExecutionLeadership(store, "postcommit-create-loss")
	leadership.ttl = time.Hour
	leadership.tick()
	if !leadership.IsAdmitted() {
		t.Fatal("test leadership was not admitted")
	}
	runner := NewTestRunner(10, NewHub(), store, WithExecutionLeadership(leadership))
	store.runner = runner
	storePreflightScript(runner, "lease-loss-create-script", storage.DefaultProjectID)
	store.blockTestCreateReturn.Store(true)
	const testID = "d84512fc-05b5-46d1-9c56-3e4302035696"

	result := make(chan managedTestCreateResult, 1)
	go func() {
		created, err := runner.CreateTest(CreateTestRequest{
			RequestedTestID: testID, Name: "postcommit lease loss", Duration: "1m",
			ScriptID: "lease-loss-create-script", VUsers: 1,
		})
		result <- managedTestCreateResult{test: created, err: err}
	}()
	select {
	case <-store.testCreateCommitted:
	case created := <-result:
		t.Fatalf("CreateTest returned before postcommit barrier: test=%#v err=%v", created.test, created.err)
	case <-time.After(2 * time.Second):
		t.Fatal("lease-fenced create did not commit before its return barrier")
	}
	leadership.relinquish()
	close(store.releaseTestCreateReturn)
	select {
	case created := <-result:
		if created.err != nil || created.test == nil || created.test.ID != testID {
			t.Fatalf("CreateTest after postcommit lease loss = (%#v, %v), want exact committed identity", created.test, created.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CreateTest did not return after postcommit lease loss")
	}
	runner.mu.RLock()
	local, ok := runner.tests[testID]
	runner.mu.RUnlock()
	if ok {
		t.Fatalf("stale leader installed committed test locally: %#v", local)
	}
	if durable, err := base.GetTest(testID); err != nil || durable == nil || durable.ID != testID {
		t.Fatalf("durable committed identity = (%#v, %v)", durable, err)
	}
}

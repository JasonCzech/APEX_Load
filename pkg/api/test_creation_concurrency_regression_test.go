package api

import (
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type blockingCreateOnlyMemoryStore struct {
	mu      sync.Mutex
	records map[string]*storage.TestRecord
	entered atomic.Int32
	release chan struct{}
}

func newBlockingCreateOnlyMemoryStore() *blockingCreateOnlyMemoryStore {
	return &blockingCreateOnlyMemoryStore{records: make(map[string]*storage.TestRecord), release: make(chan struct{})}
}

func cloneMemoryTestRecord(record *storage.TestRecord) *storage.TestRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.FolderID = cloneStringPtr(record.FolderID)
	cloned.TimeslotID = cloneStringPtr(record.TimeslotID)
	return &cloned
}

func (store *blockingCreateOnlyMemoryStore) CreateTestIfAbsent(record *storage.TestRecord) (bool, error) {
	store.entered.Add(1)
	<-store.release
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.records[record.ID] != nil {
		return false, nil
	}
	store.records[record.ID] = cloneMemoryTestRecord(record)
	return true, nil
}

func (store *blockingCreateOnlyMemoryStore) CreateTestIfAbsentIfLeaseHeld(record *storage.TestRecord, _, _ string) (bool, error) {
	return store.CreateTestIfAbsent(record)
}

func (store *blockingCreateOnlyMemoryStore) SaveTest(record *storage.TestRecord) error {
	store.mu.Lock()
	store.records[record.ID] = cloneMemoryTestRecord(record)
	store.mu.Unlock()
	return nil
}

func (store *blockingCreateOnlyMemoryStore) GetTest(id string) (*storage.TestRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record := store.records[id]
	if record == nil {
		return nil, sql.ErrNoRows
	}
	return cloneMemoryTestRecord(record), nil
}

func (store *blockingCreateOnlyMemoryStore) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (store *blockingCreateOnlyMemoryStore) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}

func (store *blockingCreateOnlyMemoryStore) UpdateTestStatus(string, string, *time.Time, string) error {
	return nil
}
func (store *blockingCreateOnlyMemoryStore) DeleteTest(string) error        { return nil }
func (store *blockingCreateOnlyMemoryStore) MoveTest(string, *string) error { return nil }

func TestConcurrentCreatesAllocateUniqueRunRefKeysWithoutSettingsStore(t *testing.T) {
	runner := NewTestRunner(10, NewHub(), nil)
	store := newBlockingCreateOnlyMemoryStore()
	runner.testStore = store
	runner.StoreScript(&dsl.Script{
		ID: "concurrent-ref-script", Name: "Concurrent ref", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	requests := []CreateTestRequest{
		{RequestedTestID: "dddf6a7d-1450-439a-b233-a3586ce4ee8e", Name: "concurrent ref one", Duration: "1m", ScriptID: "concurrent-ref-script", VUsers: 1},
		{RequestedTestID: "fe4b07d7-4208-45ed-b62d-8b2e568e9974", Name: "concurrent ref two", Duration: "1m", ScriptID: "concurrent-ref-script", VUsers: 1},
	}
	results := make(chan managedTestCreateResult, len(requests))
	for _, request := range requests {
		request := request
		go func() {
			created, err := runner.CreateTest(request)
			results <- managedTestCreateResult{test: created, err: err}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for store.entered.Load() != int32(len(requests)) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if store.entered.Load() != int32(len(requests)) {
		t.Fatalf("durable create entries = %d, want %d", store.entered.Load(), len(requests))
	}
	close(store.release)
	created := make([]*ManagedTest, 0, len(requests))
	for range requests {
		select {
		case result := <-results:
			if result.err != nil || result.test == nil {
				t.Fatalf("CreateTest = (%#v, %v)", result.test, result.err)
			}
			created = append(created, result.test)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent create did not finish")
		}
	}
	if created[0].RefKey == "" || created[1].RefKey == "" || created[0].RefKey == created[1].RefKey {
		t.Fatalf("concurrent run ref keys = %q, %q; want distinct non-empty keys", created[0].RefKey, created[1].RefKey)
	}
}

type blockingCompatibilityTestStore struct {
	mu        sync.Mutex
	record    *storage.TestRecord
	saveCalls atomic.Int32
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (store *blockingCompatibilityTestStore) SaveTest(record *storage.TestRecord) error {
	store.saveCalls.Add(1)
	store.once.Do(func() { close(store.entered) })
	<-store.release
	store.mu.Lock()
	store.record = cloneMemoryTestRecord(record)
	store.mu.Unlock()
	return nil
}

func (store *blockingCompatibilityTestStore) GetTest(id string) (*storage.TestRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.record == nil || store.record.ID != id {
		return nil, sql.ErrNoRows
	}
	return cloneMemoryTestRecord(store.record), nil
}

func (store *blockingCompatibilityTestStore) ListTests(string, *string) ([]*storage.TestRecord, error) {
	return nil, nil
}
func (store *blockingCompatibilityTestStore) ListTestsByContext(string, string, string) ([]*storage.TestRecord, error) {
	return nil, nil
}
func (store *blockingCompatibilityTestStore) UpdateTestStatus(string, string, *time.Time, string) error {
	return nil
}
func (store *blockingCompatibilityTestStore) DeleteTest(string) error        { return nil }
func (store *blockingCompatibilityTestStore) MoveTest(string, *string) error { return nil }

func TestCompatibilityCreateFallbackSerializesIdentityWithoutRunnerLock(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := &blockingCompatibilityTestStore{entered: make(chan struct{}), release: make(chan struct{})}
	runner.testStore = store
	first := &ManagedTest{ID: "compat-create-race", RefKey: "RUN-FIRST", MaterializationKey: "materialization-one", Name: "first", Status: "QUEUED", ProjectID: storage.DefaultProjectID}
	second := &ManagedTest{ID: first.ID, RefKey: "RUN-SECOND", MaterializationKey: "materialization-two", Name: "second", Status: "QUEUED", ProjectID: storage.DefaultProjectID}
	results := make(chan error, 2)
	go func() { results <- runner.saveNewManagedTestRecordWithTerm(first, executionLeaseTerm{}) }()
	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("compatibility save did not start")
	}
	go func() { results <- runner.saveNewManagedTestRecordWithTerm(second, executionLeaseTerm{}) }()
	time.Sleep(50 * time.Millisecond)
	if got := store.saveCalls.Load(); got != 1 {
		t.Fatalf("concurrent compatibility SaveTest calls = %d, want one serialized writer", got)
	}
	lockAcquired := make(chan struct{})
	go func() {
		runner.mu.Lock()
		close(lockAcquired)
		runner.mu.Unlock()
	}()
	select {
	case <-lockAcquired:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("compatibility storage write held runner.mu")
	}
	close(store.release)
	firstErr := <-results
	secondErr := <-results
	if (firstErr == nil) == (secondErr == nil) {
		t.Fatalf("compatibility results = (%v, %v), want exactly one winner", firstErr, secondErr)
	}
	loserErr := firstErr
	if loserErr == nil {
		loserErr = secondErr
	}
	if !errors.Is(loserErr, errTestIdentityCollision) {
		t.Fatalf("loser error = %v, want identity collision", loserErr)
	}
	if got := store.saveCalls.Load(); got != 1 {
		t.Fatalf("compatibility SaveTest calls = %d, want one", got)
	}
}

func TestCompatibilityCreateFallbackRejectsParentRevisionEvidence(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := &blockingCompatibilityTestStore{entered: make(chan struct{}), release: make(chan struct{})}
	runner.testStore = store
	test := &ManagedTest{
		ID: "compat-parent-fence", Name: "compat parent fence", Status: "QUEUED", ProjectID: storage.DefaultProjectID,
		expectedProjectUpdatedAt: time.Now().UTC(),
	}
	err := runner.saveNewManagedTestRecordWithTerm(test, executionLeaseTerm{})
	if err == nil || err.Error() != "test persistence does not support atomic parent-revision validation" {
		t.Fatalf("fallback parent-revision error = %v", err)
	}
	if store.saveCalls.Load() != 0 {
		t.Fatal("fallback store was written despite unsupported parent-revision evidence")
	}
}

func TestCreateOnlyRetryAdoptsExactDurableQueuedIdentity(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := newBlockingCreateOnlyMemoryStore()
	close(store.release)
	store.records["durable-adoption"] = &storage.TestRecord{
		ID: "durable-adoption", RefKey: "LOC-RUN-20260805-0042", MaterializationKey: "materialization",
		Name: "durable winner", Status: "QUEUED", ProjectID: "project-a", StartTime: time.Now().UTC(),
	}
	runner.testStore = store
	candidate := &ManagedTest{
		ID: "durable-adoption", RefKey: "LOC-RUN-20260805-0043", MaterializationKey: "materialization",
		Name: "rebuilt candidate", Status: "QUEUED", ProjectID: "project-a",
	}
	if err := runner.saveNewManagedTestRecordWithTerm(candidate, executionLeaseTerm{}); err != nil {
		t.Fatalf("saveNewManagedTestRecordWithTerm: %v", err)
	}
	if candidate.RefKey != "LOC-RUN-20260805-0042" || candidate.Name != "durable winner" || !candidate.creationAdopted {
		t.Fatalf("adopted candidate = %#v, want exact durable identity", candidate)
	}
}

func TestCreateOnlyRetryRejectsCrossProjectMaterializationCollision(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := newBlockingCreateOnlyMemoryStore()
	close(store.release)
	store.records["cross-project"] = &storage.TestRecord{
		ID: "cross-project", RefKey: "LOC-RUN-20260805-0042", MaterializationKey: "shared-key",
		Status: "QUEUED", ProjectID: "project-a", StartTime: time.Now().UTC(),
	}
	runner.testStore = store
	candidate := &ManagedTest{
		ID: "cross-project", RefKey: "LOC-RUN-20260805-0043", MaterializationKey: "shared-key",
		Status: "QUEUED", ProjectID: "project-b",
	}
	err := runner.saveNewManagedTestRecordWithTerm(candidate, executionLeaseTerm{})
	if !errors.Is(err, errTestIdentityCollision) {
		t.Fatalf("cross-project retry error = %v, want identity collision", err)
	}
}

func TestCreateOnlyRetryDefersActiveDurableRowToLeaderReconciliation(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	store := newBlockingCreateOnlyMemoryStore()
	close(store.release)
	store.records["active-adoption"] = &storage.TestRecord{
		ID: "active-adoption", RefKey: "LOC-RUN-20260805-0042", MaterializationKey: "materialization",
		Status: "RUNNING", ProjectID: "project-a", StartTime: time.Now().UTC(),
	}
	runner.testStore = store
	candidate := &ManagedTest{
		ID: "active-adoption", RefKey: "LOC-RUN-20260805-0043", MaterializationKey: "materialization",
		Status: "QUEUED", ProjectID: "project-a",
	}
	err := runner.saveNewManagedTestRecordWithTerm(candidate, executionLeaseTerm{})
	if !errors.Is(err, errDurableActiveTestRequiresReconciliation) {
		t.Fatalf("active-row retry error = %v, want leader reconciliation", err)
	}
	if candidate.Status != "QUEUED" {
		t.Fatalf("active durable row was installed without execution handles: %#v", candidate)
	}
}

type missingProjectEvidenceStore struct{}

func (missingProjectEvidenceStore) GetProject(string) (*storage.Project, error) {
	return nil, sql.ErrNoRows
}

func TestCreateTestRejectsMissingDurableProjectEvidence(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	runner.projectStore = missingProjectEvidenceStore{}
	runner.StoreScript(&dsl.Script{
		ID: "missing-project-script", Name: "Missing project", Protocol: "http", ProjectID: "missing-project",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "missing project", ProjectID: "missing-project", Duration: "1m", ScriptID: "missing-project-script", VUsers: 1,
	})
	if err == nil || created != nil || err.Error() != "project quota evidence changed or is unavailable; retry test creation" {
		t.Fatalf("CreateTest = (%#v, %v), want missing-project evidence rejection", created, err)
	}
}

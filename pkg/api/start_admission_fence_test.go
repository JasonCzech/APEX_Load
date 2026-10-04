package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

type shakeoutActionProtocol struct {
	name    string
	execute func(context.Context) error
}

func (p *shakeoutActionProtocol) Name() string { return p.name }
func (p *shakeoutActionProtocol) Init(context.Context, map[string]any) error {
	return nil
}
func (p *shakeoutActionProtocol) Execute(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	err := p.execute(ctx)
	return &protocol.Result{ActionName: action.Name, StartTime: time.Now(), Success: err == nil, StatusCode: 200}, err
}
func (p *shakeoutActionProtocol) Teardown(context.Context) error    { return nil }
func (p *shakeoutActionProtocol) HealthCheck(context.Context) error { return nil }

type admissionChurnReservationStore struct {
	runner *TestRunner
	testID string
	calls  int
	// remaining bounds deliberate fence invalidations so the asynchronous
	// recovery worker can eventually observe a stable snapshot.
	remaining int
	mu        sync.Mutex
}

type versionedShakeoutSecretStore struct {
	mu       sync.Mutex
	value    string
	metadata secretstore.Metadata
	err      error
	barrier  *shakeoutFingerprintReadBarrier
}

type shakeoutFingerprintReadBarrier struct {
	arrivals atomic.Int32
	ready    chan struct{}
	release  chan struct{}
	once     sync.Once
}

type shakeoutAdmissionBarrierSecretStore struct {
	arrivals atomic.Int32
	ready    chan struct{}
	release  chan struct{}
	once     sync.Once
}

type fixedShakeoutProjectQuotaStore struct {
	project storage.Project
}

func (s *fixedShakeoutProjectQuotaStore) GetProject(id string) (*storage.Project, error) {
	if normalizeProjectID(id) != normalizeProjectID(s.project.ID) {
		return nil, nil
	}
	project := s.project
	return &project, nil
}

func (s *shakeoutAdmissionBarrierSecretStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	if s.arrivals.Add(1) >= 2 {
		s.once.Do(func() { close(s.ready) })
	}
	<-s.release
	return "barrier-secret", secretstore.Metadata{
		ProjectID: storage.DefaultProjectID, Name: "TOKEN", Provider: "barrier", UpdatedAt: time.Unix(100, 0).UTC(),
	}, nil
}

func (*shakeoutAdmissionBarrierSecretStore) Create(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrReadOnly
}

func (*shakeoutAdmissionBarrierSecretStore) Replace(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrReadOnly
}

func (*shakeoutAdmissionBarrierSecretStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return []secretstore.Metadata{}, nil
}

func (*shakeoutAdmissionBarrierSecretStore) Writable() bool   { return false }
func (*shakeoutAdmissionBarrierSecretStore) Provider() string { return "barrier" }

func (s *versionedShakeoutSecretStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	s.mu.Lock()
	value := s.value
	metadata := s.metadata
	err := s.err
	barrier := s.barrier
	s.mu.Unlock()
	if barrier != nil {
		if barrier.arrivals.Add(1) >= 2 {
			barrier.once.Do(func() { close(barrier.ready) })
		}
		<-barrier.release
	}
	if err != nil {
		return "", secretstore.Metadata{}, err
	}
	if value == "" {
		return "", secretstore.Metadata{}, secretstore.ErrNotFound
	}
	return value, metadata, nil
}

func TestConcurrentShakeoutStartsReserveAdmissionBeforeOneShotActions(t *testing.T) {
	secrets := &shakeoutAdmissionBarrierSecretStore{ready: make(chan struct{}), release: make(chan struct{})}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "shakeout-admission-race", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "shakeout-admission-race", execute: func(context.Context) error {
			executions.Add(1)
			return nil
		}}
	})
	backend := &executionBackendSpy{called: make(chan string, 2)}
	runner := NewTestRunner(1, NewHub(), nil, WithProjectSecretStore(secrets), WithExecutionBackend(backend))
	runner.StoreScript(&dsl.Script{
		ID: "shakeout-admission-race-script", Name: "Shakeout admission race", Protocol: protocolName, Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "one shot", Type: "request", Target: "/", Headers: map[string]string{
			"Authorization": "Bearer {{secret.TOKEN}}",
		}}},
	})
	testIDs := make([]string, 0, 2)
	for index := 0; index < 2; index++ {
		test, err := runner.CreateTest(CreateTestRequest{
			Name: "shakeout admission race", ScriptID: "shakeout-admission-race-script", VUsers: 1, Duration: "1m", ScriptShakeout: true,
		})
		if err != nil {
			t.Fatalf("CreateTest[%d]: %v", index, err)
		}
		testIDs = append(testIDs, test.ID)
	}
	results := make(chan error, 2)
	for _, id := range testIDs {
		go func(testID string) { results <- runner.StartTest(testID) }(id)
	}
	select {
	case <-secrets.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent starts did not both cross initial admission")
	}
	close(secrets.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("StartTest: %v", err)
		}
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout one-shot executions = %d, want exactly one", got)
	}
	pending := 0
	started := 0
	for _, id := range testIDs {
		current, ok := runner.GetTest(id)
		if !ok {
			t.Fatalf("test %s disappeared", id)
		}
		switch current.Status {
		case "PENDING":
			pending++
		case "INITIALIZING", "RUNNING":
			started++
		default:
			t.Fatalf("test %s status = %q", id, current.Status)
		}
	}
	if pending != 1 || started != 1 {
		t.Fatalf("admission outcomes = pending %d started %d, want 1/1", pending, started)
	}
}

func TestConcurrentShakeoutStartsReserveProjectVUserQuotaBeforeOneShotActions(t *testing.T) {
	secrets := &shakeoutAdmissionBarrierSecretStore{ready: make(chan struct{}), release: make(chan struct{})}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "shakeout-project-vuser-race", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "shakeout-project-vuser-race", execute: func(context.Context) error {
			executions.Add(1)
			return nil
		}}
	})
	backend := &executionBackendSpy{called: make(chan string, 2)}
	runner := NewTestRunner(2, NewHub(), nil, WithProjectSecretStore(secrets), WithExecutionBackend(backend))
	runner.projectStore = &fixedShakeoutProjectQuotaStore{project: storage.Project{
		ID: storage.DefaultProjectID, Name: "Default", CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
		Settings: storage.ProjectSettings{MaxConcurrentTests: 2, MaxVUsers: 1},
	}}
	runner.StoreScript(&dsl.Script{
		ID: "shakeout-project-vuser-race-script", Name: "Shakeout project VUser race", Protocol: protocolName, Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "one shot", Type: "request", Target: "/", Headers: map[string]string{
			"Authorization": "Bearer {{secret.TOKEN}}",
		}}},
	})
	testIDs := make([]string, 0, 2)
	for index := 0; index < 2; index++ {
		test, err := runner.CreateTest(CreateTestRequest{
			Name: "shakeout project VUser race", ScriptID: "shakeout-project-vuser-race-script", VUsers: 1, Duration: "1m", ScriptShakeout: true,
		})
		if err != nil {
			t.Fatalf("CreateTest[%d]: %v", index, err)
		}
		testIDs = append(testIDs, test.ID)
	}
	results := make(chan error, 2)
	for _, id := range testIDs {
		go func(testID string) { results <- runner.StartTest(testID) }(id)
	}
	select {
	case <-secrets.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent starts did not both cross initial admission")
	}
	close(secrets.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("StartTest: %v", err)
		}
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout one-shot executions = %d, want exactly one", got)
	}
	pending := 0
	started := 0
	for _, id := range testIDs {
		current, ok := runner.GetTest(id)
		if !ok {
			t.Fatalf("test %s disappeared", id)
		}
		switch current.Status {
		case "PENDING":
			pending++
			if !strings.Contains(current.PendingReason, "project VUser") || !strings.Contains(current.PendingReason, "max vusers") {
				t.Fatalf("pending reason = %q, want bounded project VUser capacity detail", current.PendingReason)
			}
		case "INITIALIZING", "RUNNING":
			started++
		default:
			t.Fatalf("test %s status = %q", id, current.Status)
		}
	}
	if pending != 1 || started != 1 {
		t.Fatalf("admission outcomes = pending %d started %d, want 1/1", pending, started)
	}
}

func (*versionedShakeoutSecretStore) Create(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrReadOnly
}

func (*versionedShakeoutSecretStore) Replace(context.Context, string, string, string, string) (secretstore.Metadata, error) {
	return secretstore.Metadata{}, secretstore.ErrReadOnly
}

func (s *versionedShakeoutSecretStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.metadata.Name) == "" {
		return []secretstore.Metadata{}, nil
	}
	return []secretstore.Metadata{s.metadata}, nil
}

func (*versionedShakeoutSecretStore) Writable() bool   { return false }
func (*versionedShakeoutSecretStore) Provider() string { return "versioned-test" }

type scriptedGitHubCheckReadStore struct {
	storage.GitHubEnterpriseStore
	mu        sync.Mutex
	link      *storage.GitHubCheckLink
	failReads int
	err       error
}

func (s *scriptedGitHubCheckReadStore) GetGitHubCheckLink(string) (*storage.GitHubCheckLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failReads > 0 {
		s.failReads--
		return nil, s.err
	}
	if s.link == nil {
		return nil, nil
	}
	cloned := *s.link
	return &cloned, nil
}

func (s *admissionChurnReservationStore) GetCapacityReservation(string) (*storage.CapacityReservation, error) {
	return nil, nil
}

func (s *admissionChurnReservationStore) UpdateCapacityReservationBinding(string, *string, *string, time.Time) (bool, error) {
	return false, nil
}

func (s *admissionChurnReservationStore) ClearCapacityReservationBinding(string, string, time.Time) (bool, error) {
	return false, nil
}

func (s *admissionChurnReservationStore) ListCapacityReservationsByTimeRange(string, time.Time, time.Time) ([]*storage.CapacityReservation, error) {
	s.mu.Lock()
	s.calls++
	mutate := s.remaining > 0
	if mutate {
		s.remaining--
	}
	s.mu.Unlock()
	if !mutate {
		return nil, nil
	}
	s.runner.mu.Lock()
	if test := s.runner.tests[s.testID]; test != nil {
		test.TotalVUsers++
		if len(test.Groups) > 0 {
			test.Groups[0].VUsers++
		}
	}
	s.runner.mu.Unlock()
	return nil, nil
}

func (s *admissionChurnReservationStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func TestLoadStartLaunchEvidenceNormalizesReloadedCapacityAdvisory(t *testing.T) {
	spy := &executionBackendSpy{called: make(chan string, 1)}
	runner := NewTestRunner(4, NewHub(), nil, WithExecutionBackend(spy))
	storePreflightScript(runner, "reloaded-capacity-script", storage.DefaultProjectID)

	runner.mu.Lock()
	runner.tests["reloaded-queued-test"] = &ManagedTest{
		ID:          "reloaded-queued-test",
		Name:        "reloaded queued test",
		Status:      "QUEUED",
		ProjectID:   storage.DefaultProjectID,
		TotalVUsers: 1,
		Duration:    time.Minute,
		Groups: []TestGroup{{
			Name: "load", ScriptID: "reloaded-capacity-script", Protocol: "http", VUsers: 1,
		}},
	}
	runner.mu.Unlock()

	evidence, err := runner.loadStartLaunchEvidence("reloaded-queued-test", time.Now().UTC())
	if err != nil {
		t.Fatalf("loadStartLaunchEvidence: %v", err)
	}

	runner.mu.Lock()
	test := runner.tests["reloaded-queued-test"]
	if test.CapacityAdvisory == nil {
		runner.mu.Unlock()
		t.Fatal("start evidence left the reloaded test without a capacity advisory")
	}
	// Start-gate evaluation refreshes this same derived projection. Repeating
	// that refresh must not invalidate the evidence captured by the start path.
	runner.refreshCapacityAdvisoryLocked(test)
	current := runner.managedTestBuildFenceCurrentLocked(evidence.admissionFence)
	runner.mu.Unlock()
	if !current {
		t.Fatal("start evidence invalidated its own admission fence after capacity refresh")
	}

	if err := runner.StartTest("reloaded-queued-test"); err != nil {
		t.Fatalf("StartTest: %v", err)
	}
	select {
	case startedID := <-spy.called:
		if startedID != "reloaded-queued-test" {
			t.Fatalf("execution backend received %q, want reloaded-queued-test", startedID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reloaded queued test did not reach the execution backend")
	}
}

func TestQueuedCIRecoveryEventuallyRetriesPersistentAdmissionChurn(t *testing.T) {
	spy := &executionBackendSpy{called: make(chan string, 1)}
	runner := NewTestRunner(4, NewHub(), nil, WithExecutionBackend(spy))
	projectID := "project-ci-admission-churn"
	policy := ciRunSourceTestPolicy()
	runner.projectStore = &staticCIProjectReadStore{project: &storage.Project{
		ID: projectID, Settings: storage.ProjectSettings{GitHub: storage.GitHubProjectPolicy{Actions: policy}},
	}}
	storePreflightScript(runner, "ci-admission-churn-script", projectID)
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion:  ciRunSchemaVersion,
		Source:         ciTestRunSource("1"),
		ScriptIdentity: "script_id:ci-admission-churn-script",
		PolicyRevision: policy.Revision,
		IdempotencySHA: sha256String("persistent-admission-churn"),
	})
	if err != nil {
		t.Fatalf("encodeCIRunMetadata: %v", err)
	}
	created, err := runner.CreateTest(CreateTestRequest{
		Name: "persistent admission churn", ProjectID: projectID, ScriptID: "ci-admission-churn-script",
		VUsers: 1, Duration: "1m", MaterializationKey: metadata,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	churn := &admissionChurnReservationStore{runner: runner, testID: created.ID, remaining: startAdmissionPhaseAttempts}
	runner.reservationStore = churn

	err = runner.recoverQueuedCIRun(created.ID)
	if !errors.Is(err, errStartAdmissionEvidenceStale) {
		t.Fatalf("recoverQueuedCIRun error = %v, want typed stale-evidence error", err)
	}
	if calls := churn.callCount(); calls != startAdmissionPhaseAttempts {
		t.Fatalf("admission evidence reads = %d, want %d bounded in-request attempts", calls, startAdmissionPhaseAttempts)
	}
	recovered, ok := runner.GetTest(created.ID)
	if !ok || recovered.Status != "QUEUED" || recovered.StartedAt != nil || recovered.Error != "" {
		t.Fatalf("persistently churning run was not left queued: %#v", recovered)
	}
	select {
	case startedID := <-spy.called:
		if startedID != created.ID {
			t.Fatalf("recovery backend received %q, want %q", startedID, created.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("term-bound CI recovery worker did not retry the queued run after admission stabilized")
	}
}

func TestStaleStartEvidenceDoesNotPoisonNextLeaseClaim(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "start-evidence-lease.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	leadership := newExecutionLeadership(store, "start-evidence-lease")
	leadership.tick()
	t.Cleanup(leadership.relinquish)
	runner := NewTestRunner(4, NewHub(), store, WithExecutionLeadership(leadership))
	storePreflightScript(runner, "stale-lease-script", storage.DefaultProjectID)
	oldTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture old lease term: %v", err)
	}

	const testID = "stale-evidence-lease-test"
	runner.mu.Lock()
	runner.tests[testID] = &ManagedTest{
		ID: testID, Name: "stale evidence lease", Status: "QUEUED",
		ProjectID: storage.DefaultProjectID, TotalVUsers: 1, Duration: time.Minute, CreatedAt: time.Now().UTC(),
		Groups: []TestGroup{{Name: "load", ScriptID: "stale-lease-script", Protocol: "http", VUsers: 1}},
	}
	runner.mu.Unlock()
	evidence, err := runner.loadStartLaunchEvidence(testID, time.Now().UTC())
	if err != nil {
		t.Fatalf("loadStartLaunchEvidence: %v", err)
	}

	runner.mu.Lock()
	test := runner.tests[testID]
	test.TotalVUsers = 2
	test.Groups[0].VUsers = 2
	blocked, gateErr := runner.preflightStartBlocksLockedWithExpectedTermAndEvidence(
		testID, StartTestOptions{}, &oldTerm, time.Now().UTC(), evidence,
	)
	claimedFence := test.executionFence
	runner.mu.Unlock()
	if blocked || !errors.Is(gateErr, errStartAdmissionEvidenceStale) {
		t.Fatalf("stale preflight = (blocked=%t, err=%v), want typed stale-evidence rejection", blocked, gateErr)
	}
	if claimedFence != "" {
		t.Fatalf("stale evidence claimed old execution fence %q", claimedFence)
	}

	leadership.relinquish()
	leadership.tick()
	newTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture reacquired lease term: %v", err)
	}
	if newTerm.ownerID == oldTerm.ownerID || newTerm.generation == oldTerm.generation {
		t.Fatalf("lease term did not rotate: old=%#v new=%#v", oldTerm, newTerm)
	}
	runner.mu.Lock()
	claimErr := runner.claimTestExecutionFenceWithTerm(test, newTerm)
	claimedFence = test.executionFence
	runner.mu.Unlock()
	if claimErr != nil {
		t.Fatalf("successor lease could not claim queued test: %v", claimErr)
	}
	if claimedFence != newTerm.ownerID {
		t.Fatalf("queued test fence = %q, want successor %q", claimedFence, newTerm.ownerID)
	}
}

func TestCIRunRefKeyPolicyRevocationPrecedesShakeout(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	projectID := "project-ci-refkey-revocation"
	policy := ciRunSourceTestPolicy()
	project := &storage.Project{
		ID: projectID, Name: "CI refkey", UpdatedAt: time.Now().UTC(),
		Settings: storage.ProjectSettings{GitHub: storage.GitHubProjectPolicy{Actions: policy}},
	}
	runner.projectStore = &staticCIProjectReadStore{project: project}
	runner.StoreScript(&dsl.Script{
		ID: "ci-refkey-shakeout-script", Name: "CI refkey shakeout", ProjectID: projectID,
		Protocol: "unsupported-ci-refkey-shakeout", Version: "1.0.0",
		Actions: []dsl.Action{{Name: "would enter review", Type: "request", Target: "/"}},
	})
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"),
		ScriptIdentity: "script_id:ci-refkey-shakeout-script", PolicyRevision: policy.Revision,
		IdempotencySHA: sha256String("ci-refkey-revocation"),
	})
	if err != nil {
		t.Fatal(err)
	}
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "CI refkey revocation", ProjectID: projectID, ScriptID: "ci-refkey-shakeout-script",
		VUsers: 1, Duration: "1m", ScriptShakeout: true, MaterializationKey: metadata,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	project.Settings.GitHub.Actions.Enabled = false
	project.UpdatedAt = project.UpdatedAt.Add(time.Second)

	err = runner.StartTest(test.RefKey)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("StartTest(refkey) error = %v, want revoked CI policy", err)
	}
	current, ok := runner.GetTest(test.ID)
	if !ok || current.Status != "QUEUED" || current.Error != "" {
		t.Fatalf("revoked refkey run executed shakeout side effects: %#v", current)
	}
	if current.StartAuthorization != nil && current.StartAuthorization.ScriptShakeoutPass != nil {
		t.Fatalf("revoked refkey run retained a shakeout pass marker: %#v", current.StartAuthorization)
	}
}

func TestConcurrentTerminalizerFenceCannotBeAdoptedByStart(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "terminalizer-start-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	leadership := newExecutionLeadership(store, "terminalizer-start-race")
	leadership.tick()
	t.Cleanup(leadership.relinquish)
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership))
	storePreflightScript(runner, "terminalizer-start-race-script", storage.DefaultProjectID)
	policy := ciRunSourceTestPolicy()
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"),
		ScriptIdentity: "script_id:terminalizer-start-race-script", PolicyRevision: policy.Revision,
		IdempotencySHA: sha256String("terminalizer-start-race"),
	})
	if err != nil {
		t.Fatal(err)
	}
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "terminalizer start race", ScriptID: "terminalizer-start-race-script",
		VUsers: 1, Duration: "1m", MaterializationKey: metadata,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	term, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	evidenceAt := time.Now().UTC()
	evidence, err := runner.loadStartLaunchEvidence(test.ID, evidenceAt)
	if err != nil {
		t.Fatal(err)
	}

	claimed := make(chan struct{})
	releaseTerminalizer := make(chan struct{})
	terminalDone := make(chan error, 1)
	go func() {
		runner.mu.Lock()
		current := runner.tests[test.ID]
		_, claimErr := runner.claimPassiveExecutionFenceLocked(current, &term)
		runner.mu.Unlock()
		if claimErr != nil {
			terminalDone <- claimErr
			return
		}
		close(claimed)
		<-releaseTerminalizer
		terminalDone <- runner.completeTestRunWithPendingPromotion(current, "FAILED", time.Now().UTC(), "terminalizer won", nil, false)
	}()
	select {
	case <-claimed:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal transition did not claim its passive-row fence")
	}

	runner.mu.Lock()
	ownedFence := runner.tests[test.ID].executionFence
	blocked, preflightErr := runner.preflightStartBlocksLockedWithExpectedTermAndEvidence(test.ID, StartTestOptions{}, &term, evidenceAt, evidence)
	afterPreflightFence := runner.tests[test.ID].executionFence
	_, prepareErr := runner.prepareTestStartLockedWithExpectedTermAndEvidence(test.ID, StartTestOptions{}, &term, evidenceAt, evidence)
	afterPrepareStatus := runner.tests[test.ID].Status
	afterPrepareFence := runner.tests[test.ID].executionFence
	runner.mu.Unlock()
	if blocked || preflightErr != nil {
		t.Fatalf("read-only preflight = (blocked=%t, err=%v)", blocked, preflightErr)
	}
	if ownedFence == "" || afterPreflightFence != ownedFence {
		t.Fatalf("preflight changed terminalizer fence from %q to %q", ownedFence, afterPreflightFence)
	}
	if !errors.Is(prepareErr, errStartAdmissionEvidenceStale) || afterPrepareStatus != "QUEUED" || afterPrepareFence != ownedFence {
		t.Fatalf("concurrent prepare = (status=%q fence=%q err=%v), want QUEUED with terminalizer fence and stale error", afterPrepareStatus, afterPrepareFence, prepareErr)
	}

	close(releaseTerminalizer)
	select {
	case err := <-terminalDone:
		if err != nil {
			t.Fatalf("terminalizer: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminalizer did not finish")
	}
	current, _ := runner.GetTest(test.ID)
	if current.Status != "FAILED" || current.Error != "terminalizer won" {
		t.Fatalf("terminal outcome = %#v", current)
	}
}

func TestConcurrentTerminalizerClaimCannotBeAdoptedByStartWithoutHA(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	storePreflightScript(runner, "local-terminalizer-start-race-script", storage.DefaultProjectID)
	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "local terminalizer start race",
		ScriptID: "local-terminalizer-start-race-script",
		VUsers:   1,
		Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	evidenceAt := time.Now().UTC()
	evidence, err := runner.loadStartLaunchEvidence(test.ID, evidenceAt)
	if err != nil {
		t.Fatal(err)
	}

	claimed := make(chan struct{})
	releaseTerminalizer := make(chan struct{})
	terminalDone := make(chan error, 1)
	go func() {
		runner.mu.Lock()
		current := runner.tests[test.ID]
		_, claimErr := runner.claimPassiveExecutionFenceLocked(current, nil)
		runner.mu.Unlock()
		if claimErr != nil {
			terminalDone <- claimErr
			return
		}
		close(claimed)
		<-releaseTerminalizer
		terminalDone <- runner.completeTestRunWithPendingPromotion(current, "FAILED", time.Now().UTC(), "local terminalizer won", nil, false)
	}()
	select {
	case <-claimed:
	case <-time.After(2 * time.Second):
		t.Fatal("terminal transition did not claim its process-local passive-row token")
	}

	runner.mu.Lock()
	ownedToken := runner.tests[test.ID].passiveOperationToken
	blocked, preflightErr := runner.preflightStartBlocksLockedWithExpectedTermAndEvidence(test.ID, StartTestOptions{}, nil, evidenceAt, evidence)
	afterPreflightToken := runner.tests[test.ID].passiveOperationToken
	_, prepareErr := runner.prepareTestStartLockedWithExpectedTermAndEvidence(test.ID, StartTestOptions{}, nil, evidenceAt, evidence)
	afterPrepareStatus := runner.tests[test.ID].Status
	afterPrepareToken := runner.tests[test.ID].passiveOperationToken
	runner.mu.Unlock()
	if blocked || preflightErr != nil {
		t.Fatalf("read-only preflight = (blocked=%t, err=%v)", blocked, preflightErr)
	}
	if ownedToken == "" || afterPreflightToken != ownedToken {
		t.Fatalf("preflight changed terminalizer token from %q to %q", ownedToken, afterPreflightToken)
	}
	if !errors.Is(prepareErr, errStartAdmissionEvidenceStale) || afterPrepareStatus != "QUEUED" || afterPrepareToken != ownedToken {
		t.Fatalf("concurrent prepare = (status=%q token=%q err=%v), want QUEUED with terminalizer token and stale error", afterPrepareStatus, afterPrepareToken, prepareErr)
	}

	close(releaseTerminalizer)
	select {
	case err := <-terminalDone:
		if err != nil {
			t.Fatalf("terminalizer: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminalizer did not finish")
	}
	runner.mu.RLock()
	localToken := runner.tests[test.ID].passiveOperationToken
	runner.mu.RUnlock()
	current, _ := runner.GetTest(test.ID)
	if current.Status != "FAILED" || current.Error != "local terminalizer won" || localToken != "" {
		t.Fatalf("terminal outcome = %#v, local token = %q", current, localToken)
	}
}

func TestCancelledPassiveTerminalCommitReleasesLocalClaimForReload(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "cancelled-passive-terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(2, NewHub(), store)
	storePreflightScript(runner, "cancelled-passive-terminal-script", storage.DefaultProjectID)
	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "cancelled passive terminal",
		ScriptID: "cancelled-passive-terminal-script",
		VUsers:   1,
		Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	runner.mu.Lock()
	current := runner.tests[test.ID]
	if _, err := runner.claimPassiveExecutionFenceLocked(current, nil); err != nil {
		runner.mu.Unlock()
		t.Fatalf("claimPassiveExecutionFenceLocked: %v", err)
	}
	runner.mu.Unlock()
	if err := store.DeleteTest(test.ID); err != nil {
		t.Fatalf("DeleteTest: %v", err)
	}
	if err := runner.completeTestRunWithPendingPromotion(current, "FAILED", time.Now().UTC(), "stale terminalizer", nil, false); err != nil {
		t.Fatalf("completeTestRunWithPendingPromotion: %v", err)
	}
	runner.mu.RLock()
	retainedToken := runner.tests[test.ID].passiveOperationToken
	runner.mu.RUnlock()
	if retainedToken != "" {
		t.Fatalf("cancelled terminal commit retained process-local token %q", retainedToken)
	}

	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("reloadStoredTests: %v", err)
	}
	runner.mu.RLock()
	_, retained := runner.tests[test.ID]
	runner.mu.RUnlock()
	if retained {
		t.Fatal("deleted durable row remained as a process-local ghost after reload")
	}
}

type startFenceOnlyTestDecorator struct {
	testPersistence
	startStore deploymentLeaseTestStartPersistence
}

type revisionBlindStorageDecorator struct{ storage.Store }

func TestDurableStartAdmissionFailsClosedWithoutRevisionReader(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "revision-blind-admission.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	blind := &revisionBlindStorageDecorator{Store: base}
	runner := NewTestRunner(2, NewHub(), blind)
	storePreflightScript(runner, "revision-blind-script", storage.DefaultProjectID)
	// This test targets launch-evidence capability detection. Use the concrete
	// store only for setup because the intentionally blind decorator also omits
	// the unrelated atomic parent-revision create capability.
	runner.testStore = base
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "revision blind", ScriptID: "revision-blind-script", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	runner.testStore = blind
	if _, err := runner.loadStartLaunchEvidence(test.ID, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "admission revision fence is unavailable") {
		t.Fatalf("revision-blind evidence error = %v, want fail-closed capability error", err)
	}
}

func (s *startFenceOnlyTestDecorator) SaveTestStartIfNoDeploymentLease(test *storage.TestRecord, leaseName, ownerID, tokenHash string) error {
	return s.startStore.SaveTestStartIfNoDeploymentLease(test, leaseName, ownerID, tokenHash)
}

func (s *startFenceOnlyTestDecorator) SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *storage.TestRecord, executionLeaseName, executionOwnerID, deploymentLeaseName, deploymentOwnerID, deploymentTokenHash string) (bool, error) {
	return s.startStore.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test, executionLeaseName, executionOwnerID, deploymentLeaseName, deploymentOwnerID, deploymentTokenHash)
}

func TestStartAdmissionRevisionCannotBeBypassedByTestStoreDecorator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decorated-start-admission.db")
	base, err := storage.NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := storage.NewSQLiteStore(path)
	if err != nil {
		_ = base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close(); _ = writer.Close() })
	runner := NewTestRunner(2, NewHub(), base)
	storePreflightScript(runner, "decorated-admission-script", storage.DefaultProjectID)
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "decorated admission", ScriptID: "decorated-admission-script", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	// This decorator deliberately exposes the start transaction but not the
	// revision reader. Evidence must keep using the concrete reader captured by
	// NewTestRunner instead of silently falling back through mutable testStore.
	runner.testStore = &startFenceOnlyTestDecorator{testPersistence: base, startStore: base}
	evidenceAt := time.Now().UTC()
	evidence, err := runner.loadStartLaunchEvidence(test.ID, evidenceAt)
	if err != nil {
		t.Fatalf("loadStartLaunchEvidence: %v", err)
	}
	if evidence.startAdmissionRevision <= 0 {
		t.Fatalf("decorated evidence omitted durable admission revision: %+v", evidence)
	}
	capacity := 1
	if err := writer.SaveCapacityReservation(&storage.CapacityReservation{
		ID: "decorated-concurrent-slot", ProjectID: storage.DefaultProjectID,
		StartAt: evidenceAt.Add(-time.Minute), EndAt: evidenceAt.Add(time.Hour), ApprovalState: "approved",
		RequestedCapacity: &storage.CapacityReservationRequest{VUsers: &capacity},
	}); err != nil {
		t.Fatalf("SaveCapacityReservation: %v", err)
	}
	runner.mu.Lock()
	_, prepareErr := runner.prepareTestStartLockedWithExpectedTermAndEvidence(test.ID, StartTestOptions{}, nil, evidenceAt, evidence)
	status := runner.tests[test.ID].Status
	runner.mu.Unlock()
	if !errors.Is(prepareErr, errStartAdmissionEvidenceStale) || status != "QUEUED" {
		t.Fatalf("decorated stale prepare = (status=%q err=%v), want QUEUED/stale", status, prepareErr)
	}
}

func TestAdmissionRetryDoesNotRepeatSuccessfulScriptShakeout(t *testing.T) {
	spy := &executionBackendSpy{called: make(chan string, 1)}
	runner := NewTestRunner(2, NewHub(), nil, WithExecutionBackend(spy))
	var executions atomic.Int32
	churn := &admissionChurnReservationStore{runner: runner}
	protocolName := registerRunnerTestProtocol(t, "shakeout-once", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "shakeout-once", execute: func(context.Context) error {
			executions.Add(1)
			churn.mu.Lock()
			churn.remaining = 1
			churn.mu.Unlock()
			return nil
		}}
	})
	runner.StoreScript(&dsl.Script{
		ID: "shakeout-once-script", Name: "Shakeout once", Protocol: protocolName, Version: "1.0.0",
		Actions: []dsl.Action{{Name: "external side effect", Type: "request", Target: "/"}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "shakeout once", ScriptID: "shakeout-once-script", VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	churn.testID = test.ID
	runner.reservationStore = churn

	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest: %v", err)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions = %d, want exactly 1 across admission retry", got)
	}
	current, _ := runner.GetTest(test.ID)
	if current.StartAuthorization == nil || !scriptShakeoutPassAllows(current.StartAuthorization.ScriptShakeoutPass) {
		t.Fatalf("successful shakeout marker was not retained: %#v", current.StartAuthorization)
	}
	select {
	case startedID := <-spy.called:
		if startedID != test.ID {
			t.Fatalf("backend started %q, want %q", startedID, test.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test did not start after the post-shakeout admission retry")
	}
}

func TestCompletedScriptShakeoutOutcomeRetriesWithoutReplayingActions(t *testing.T) {
	tests := []struct {
		name       string
		actionErr  error
		wantStatus string
	}{
		{name: "pass", wantStatus: "INITIALIZING"},
		{name: "review", actionErr: errors.New("injected shakeout failure"), wantStatus: "SHAKEOUT_REVIEW"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &executionBackendSpy{called: make(chan string, 1)}
			runner := NewTestRunner(2, NewHub(), nil, WithExecutionBackend(spy))
			runner.startAdmissionRevisionStore = &flakyStartAdmissionRevisionReader{revision: 1}
			var executions atomic.Int32
			protocolName := registerRunnerTestProtocol(t, "shakeout-durable-outcome-"+tt.name, func() protocol.Protocol {
				return &shakeoutActionProtocol{name: "shakeout-durable-outcome-" + tt.name, execute: func(context.Context) error {
					executions.Add(1)
					return tt.actionErr
				}}
			})
			runner.StoreScript(&dsl.Script{
				ID: "shakeout-durable-outcome-script-" + tt.name, Name: "Durable shakeout outcome", Protocol: protocolName, Version: "1.0.0",
				Actions: []dsl.Action{{Name: "external side effect", Type: "request", Target: "/"}},
			})
			test, err := runner.CreateTest(CreateTestRequest{
				Name: "durable shakeout outcome", ScriptID: "shakeout-durable-outcome-script-" + tt.name,
				VUsers: 1, Duration: "1m", ScriptShakeout: true,
			})
			if err != nil {
				t.Fatalf("CreateTest: %v", err)
			}
			persistenceErr := errors.New("temporary test persistence outage")
			store := &scriptedTestPersistence{
				testPersistence: failingDistributedStartStore{},
				err:             persistenceErr,
				failSaveCalls:   failCalls(2, durableTransitionAttempts),
			}
			runner.testStore = store

			firstErr := runner.StartTest(test.ID)
			if !errors.Is(firstErr, errStartAdmissionEvidenceStale) || !errors.Is(firstErr, persistenceErr) {
				t.Fatalf("first StartTest error = %v, want retryable durable outcome failure", firstErr)
			}
			runner.mu.RLock()
			current := runner.tests[test.ID]
			retained := current != nil && current.Status == "SHAKEOUT" && current.pendingScriptShakeoutOutcome != nil && current.executionCtx == nil
			runner.mu.RUnlock()
			if !retained {
				t.Fatalf("completed shakeout outcome was not retained for retry: %#v", current)
			}

			secondErr := runner.StartTest(test.ID)
			if tt.actionErr == nil {
				if secondErr != nil {
					t.Fatalf("second StartTest: %v", secondErr)
				}
				select {
				case <-spy.called:
				case <-time.After(2 * time.Second):
					t.Fatal("full run did not start after shakeout outcome retry")
				}
			} else {
				var gateErr *ScriptShakeoutGateError
				if !errors.As(secondErr, &gateErr) {
					t.Fatalf("second StartTest error = %v, want ScriptShakeoutGateError", secondErr)
				}
			}
			if got := executions.Load(); got != 1 {
				t.Fatalf("shakeout action executions = %d, want one across durable retry", got)
			}
			runner.mu.RLock()
			current = runner.tests[test.ID]
			status := current.Status
			pendingOutcome := current.pendingScriptShakeoutOutcome
			runner.mu.RUnlock()
			if status != tt.wantStatus || pendingOutcome != nil {
				t.Fatalf("final shakeout state = status %q outcome %#v, want %q with no pending outcome", status, pendingOutcome, tt.wantStatus)
			}
		})
	}
}

func TestCompletedScriptShakeoutFingerprintOutageThenRotationFailsClosedWithoutReplay(t *testing.T) {
	projectID := "project-shakeout-verification-rotation"
	providerErr := errors.New("secret provider temporarily unavailable")
	secretStore := &versionedShakeoutSecretStore{
		value: "before-shakeout",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: "TOKEN", Provider: "versioned-test", UpdatedAt: time.Unix(100, 0).UTC(),
		},
	}
	backend := &executionBackendSpy{called: make(chan string, 1)}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "shakeout-verification-rotation", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "shakeout-verification-rotation", execute: func(context.Context) error {
			executions.Add(1)
			secretStore.mu.Lock()
			secretStore.err = providerErr
			secretStore.mu.Unlock()
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), nil, WithProjectSecretStore(secretStore), WithExecutionBackend(backend))
	runner.StoreScript(&dsl.Script{
		ID: "shakeout-verification-rotation-script", Name: "Shakeout verification rotation", ProjectID: projectID,
		Protocol: protocolName, Version: "1.0.0", Actions: []dsl.Action{{
			Name: "one shot", Type: "request", Target: "/", Headers: map[string]string{"Authorization": "Bearer {{secret.TOKEN}}"},
		}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "shakeout verification rotation", ProjectID: projectID, ScriptID: "shakeout-verification-rotation-script",
		VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	firstErr := runner.StartTest(test.ID)
	if !errors.Is(firstErr, errStartAdmissionEvidenceStale) || !errors.Is(firstErr, providerErr) {
		t.Fatalf("first StartTest error = %v, want retryable post-action verification outage", firstErr)
	}
	runner.mu.RLock()
	current := runner.tests[test.ID]
	verificationPending := current != nil && current.Status == "SHAKEOUT" && current.pendingScriptShakeoutOutcome != nil &&
		current.pendingScriptShakeoutOutcome.VerificationPending && current.executionCtx == nil
	runner.mu.RUnlock()
	if !verificationPending {
		t.Fatalf("completed action did not retain private verification-pending outcome: %#v", current)
	}

	secretStore.mu.Lock()
	secretStore.err = nil
	secretStore.value = "rotated-after-shakeout"
	secretStore.metadata.UpdatedAt = time.Unix(200, 0).UTC()
	secretStore.mu.Unlock()
	secondErr := runner.StartTest(test.ID)
	var gateErr *ScriptShakeoutGateError
	if !errors.As(secondErr, &gateErr) {
		t.Fatalf("second StartTest error = %v, want ScriptShakeoutGateError after input rotation", secondErr)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions = %d, want one across verification recovery", got)
	}
	current, _ = runner.GetTest(test.ID)
	if current.Status != "SHAKEOUT_REVIEW" {
		t.Fatalf("status = %q, want SHAKEOUT_REVIEW", current.Status)
	}
	select {
	case startedID := <-backend.called:
		t.Fatalf("full execution backend started unexpectedly for %q", startedID)
	default:
	}
}

func TestConcurrentCompletedScriptShakeoutVerificationResumesDoNotReplay(t *testing.T) {
	projectID := "project-shakeout-concurrent-verification"
	providerErr := errors.New("secret provider temporarily unavailable")
	secretStore := &versionedShakeoutSecretStore{
		value: "stable-value",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: "TOKEN", Provider: "versioned-test", UpdatedAt: time.Unix(100, 0).UTC(),
		},
	}
	backend := &executionBackendSpy{called: make(chan string, 1)}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "shakeout-concurrent-verification", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "shakeout-concurrent-verification", execute: func(context.Context) error {
			executions.Add(1)
			secretStore.mu.Lock()
			secretStore.err = providerErr
			secretStore.mu.Unlock()
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), nil, WithProjectSecretStore(secretStore), WithExecutionBackend(backend))
	runner.StoreScript(&dsl.Script{
		ID: "shakeout-concurrent-verification-script", Name: "Concurrent shakeout verification", ProjectID: projectID,
		Protocol: protocolName, Version: "1.0.0", Actions: []dsl.Action{{
			Name: "one shot", Type: "request", Target: "/", Headers: map[string]string{"Authorization": "Bearer {{secret.TOKEN}}"},
		}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "concurrent shakeout verification", ProjectID: projectID, ScriptID: "shakeout-concurrent-verification-script",
		VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if err := runner.StartTest(test.ID); !errors.Is(err, errStartAdmissionEvidenceStale) {
		t.Fatalf("first StartTest error = %v, want retryable verification outage", err)
	}

	barrier := &shakeoutFingerprintReadBarrier{ready: make(chan struct{}), release: make(chan struct{})}
	secretStore.mu.Lock()
	secretStore.err = nil
	secretStore.barrier = barrier
	secretStore.mu.Unlock()
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- runner.resumePendingScriptShakeoutOutcome(test.ID, nil) }()
	}
	select {
	case <-barrier.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent verification retries did not both reach provider read")
	}
	close(barrier.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("resumePendingScriptShakeoutOutcome: %v", err)
		}
	}
	secretStore.mu.Lock()
	secretStore.barrier = nil
	secretStore.mu.Unlock()

	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions = %d, want one across concurrent verification", got)
	}
	runner.mu.RLock()
	current := runner.tests[test.ID]
	status := current.Status
	outcome := current.pendingScriptShakeoutOutcome
	runner.mu.RUnlock()
	if status != "QUEUED" || outcome != nil {
		t.Fatalf("verification result = status %q outcome %#v, want QUEUED with no private outcome", status, outcome)
	}
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest after verified recovery: %v", err)
	}
	select {
	case startedID := <-backend.called:
		if startedID != test.ID {
			t.Fatalf("backend started %q, want %q", startedID, test.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("full run did not start after verification recovery")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after launch = %d, want no replay", got)
	}
}

func TestSameValueRuntimeOverlayProviderOutageDefersVerificationWithoutReplay(t *testing.T) {
	projectID := storage.DefaultProjectID
	providerErr := errors.New("provider metadata temporarily unavailable")
	secretStore := &versionedShakeoutSecretStore{
		value: "provider-and-overlay-value",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: "TOKEN", Provider: "versioned-test", UpdatedAt: time.Unix(100, 0).UTC(),
		},
	}
	backend := &executionBackendSpy{called: make(chan string, 1)}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "shakeout-overlay-provider-outage", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "shakeout-overlay-provider-outage", execute: func(context.Context) error {
			executions.Add(1)
			secretStore.mu.Lock()
			secretStore.err = providerErr
			secretStore.mu.Unlock()
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), nil, WithProjectSecretStore(secretStore), WithExecutionBackend(backend))
	runner.StoreScript(&dsl.Script{
		ID: "shakeout-overlay-provider-outage-script", Name: "Overlay provider outage", ProjectID: projectID,
		Protocol: protocolName, Version: "1.0.0", Actions: []dsl.Action{{
			Name: "one shot", Type: "request", Target: "/", Headers: map[string]string{"Authorization": "Bearer {{secret.TOKEN}}"},
		}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "overlay provider outage", ProjectID: projectID, ScriptID: "shakeout-overlay-provider-outage-script",
		VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	runner.mu.Lock()
	managed := runner.tests[test.ID]
	managed.Groups[0].runtimeSecretValues = map[string]string{"TOKEN": "provider-and-overlay-value"}
	managed.groupSpecs[0].SecretResolver = overlayRuntimeSecretResolver(
		managed.Groups[0].runtimeSecretValues,
		runner.secretResolverForProject(projectID),
	)
	runner.mu.Unlock()

	// This test owns the explicit manual retry. Automatic recovery has its own
	// no-second-request regression and must not race this Start assertion.
	firstErr := runner.StartTestWithOptions(test.ID, StartTestOptions{deferPostShakeoutRecoveryEnqueue: true})
	if !errors.Is(firstErr, errStartAdmissionEvidenceStale) || !errors.Is(firstErr, providerErr) {
		t.Fatalf("first StartTest error = %v, want retryable provider-evidence outage", firstErr)
	}
	runner.mu.RLock()
	current := runner.tests[test.ID]
	verificationPending := current != nil && current.Status == "SHAKEOUT" && current.pendingScriptShakeoutOutcome != nil &&
		current.pendingScriptShakeoutOutcome.VerificationPending && current.executionCtx == nil
	runner.mu.RUnlock()
	if !verificationPending {
		t.Fatalf("same-value overlay outage state = %#v, want verification pending", current)
	}

	secretStore.mu.Lock()
	secretStore.err = nil
	secretStore.mu.Unlock()
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatalf("StartTest after provider recovery: %v", err)
	}
	select {
	case startedID := <-backend.called:
		if startedID != test.ID {
			t.Fatalf("backend started %q, want %q", startedID, test.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("full run did not start after provider evidence recovered")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions = %d, want one across provider recovery", got)
	}
}

func TestScriptShakeoutPassFingerprintBindsExecutableAndSecretSourceVersion(t *testing.T) {
	projectID := "project-versioned-shakeout"
	secretStore := &versionedShakeoutSecretStore{
		value: "resolved-value-never-hashed",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: "API_TOKEN", Provider: "versioned-test", UpdatedAt: time.Unix(100, 0).UTC(),
		},
	}
	runner := NewTestRunner(2, NewHub(), nil, WithProjectSecretStore(secretStore))
	runner.StoreScript(&dsl.Script{
		ID: "versioned-shakeout-script", Name: "Versioned shakeout", ProjectID: projectID, Protocol: "http", Version: "1.0.0",
		Config:     dsl.ScriptConfig{BaseURL: "https://example.test"},
		Parameters: []dsl.Parameter{{Name: "region", Type: "list", Values: []string{"east"}}},
		Actions:    []dsl.Action{{Name: "request", Type: "request", Target: "/", Headers: map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}"}}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "versioned shakeout", ProjectID: projectID, ScriptID: "versioned-shakeout-script", VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	first, err := runner.scriptShakeoutExecutableFingerprint(test.ID)
	if err != nil {
		t.Fatalf("first fingerprint: %v", err)
	}
	secretStore.mu.Lock()
	secretStore.value = "different-resolved-value-still-never-hashed"
	secretStore.mu.Unlock()
	valueOnly, err := runner.scriptShakeoutExecutableFingerprint(test.ID)
	if err != nil {
		t.Fatalf("value-only fingerprint: %v", err)
	}
	if valueOnly.EvidenceSHA256 != first.EvidenceSHA256 {
		t.Fatal("durable shakeout evidence changed with secret plaintext despite unchanged source version")
	}
	if valueOnly.ProcessSHA256 == first.ProcessSHA256 {
		t.Fatal("process-private shakeout proof did not bind the resolved secret plaintext")
	}
	runner.mu.Lock()
	runner.tests[test.ID].Groups[0].script.Parameters[0].Values[0] = "west"
	runner.tests[test.ID].groupSpecs[0].Script.Parameters[0].Values[0] = "west"
	runner.mu.Unlock()
	second, err := runner.scriptShakeoutExecutableFingerprint(test.ID)
	if err != nil {
		t.Fatalf("parameter fingerprint: %v", err)
	}
	if second.EvidenceSHA256 == first.EvidenceSHA256 || second.ProcessSHA256 == valueOnly.ProcessSHA256 {
		t.Fatal("parameter edit did not change executable shakeout fingerprint")
	}
	secretStore.mu.Lock()
	secretStore.metadata.UpdatedAt = secretStore.metadata.UpdatedAt.Add(time.Second)
	secretStore.mu.Unlock()
	third, err := runner.scriptShakeoutExecutableFingerprint(test.ID)
	if err != nil {
		t.Fatalf("rotated-secret fingerprint: %v", err)
	}
	if third.EvidenceSHA256 == second.EvidenceSHA256 {
		t.Fatal("secret source version change did not change executable shakeout fingerprint")
	}
	secretStore.mu.Lock()
	secretStore.metadata.UpdatedAt = time.Time{}
	secretStore.mu.Unlock()
	unversioned, err := runner.scriptShakeoutExecutableFingerprint(test.ID)
	if err != nil {
		t.Fatalf("unversioned secret source fingerprint: %v", err)
	}
	if unversioned.ReusableAfterRestart {
		t.Fatal("unversioned secret source produced restart-reusable shakeout evidence")
	}
	if unversioned.ProcessSHA256 == "" {
		t.Fatal("unversioned secret source did not produce a live-process shakeout proof")
	}
}

func TestUnversionedRuntimeSecretShakeoutProofIsLiveTermOnlyAndNeverReplayedAfterRestart(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "unversioned-runtime-shakeout.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: store}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "unversioned-runtime-shakeout", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "unversioned-runtime-shakeout", execute: func(context.Context) error {
			executions.Add(1)
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), store, WithProjectSecretStore(secrets))
	t.Cleanup(runner.beginShutdown)
	runner.StoreScript(&dsl.Script{
		ID: "unversioned-runtime-shakeout-script", Name: "Unversioned runtime shakeout", Protocol: protocolName, Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "external side effect", Type: "request", Target: "/"}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "unversioned runtime shakeout", ScriptID: "unversioned-runtime-shakeout-script", VUsers: 1, Duration: "1m", ScriptShakeout: true,
		RuntimeSettings: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{
			Type: "bearer", Token: "runtime-credential-never-persisted-in-the-test",
		}}},
	})
	if err != nil {
		t.Fatalf("CreateTest with governed runtime secret: %v", err)
	}

	shakeoutTest, err := runner.beginScriptShakeoutIfNeeded(test.ID, StartTestOptions{})
	if err != nil {
		t.Fatalf("beginScriptShakeoutIfNeeded: %v", err)
	}
	if shakeoutTest == nil {
		t.Fatal("script shakeout was not required")
	}
	report, err := runner.runScriptShakeoutReport(shakeoutTest)
	if err != nil {
		t.Fatalf("runScriptShakeoutReport: %v", err)
	}
	if !report.Passed {
		t.Fatalf("script shakeout report = %#v, want pass", report)
	}
	if err := runner.markScriptShakeoutPassed(test.ID, report); err != nil {
		t.Fatalf("markScriptShakeoutPassed: %v", err)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions = %d, want one", got)
	}
	validated, err := runner.validateScriptShakeoutPassForStart(test.ID, StartTestOptions{}, nil)
	if err != nil || !validated {
		t.Fatalf("same-process pass validation = (%t, %v), want true", validated, err)
	}
	current, _ := runner.GetTest(test.ID)
	if current.StartAuthorization == nil || current.StartAuthorization.ScriptShakeoutPass == nil ||
		current.StartAuthorization.ScriptShakeoutPass.ReusableAfterRestart {
		t.Fatalf("durable unversioned pass evidence = %#v, want non-reusable marker", current.StartAuthorization)
	}

	backend := &executionBackendSpy{called: make(chan string, 1)}
	reloaded := NewTestRunner(2, NewHub(), store, WithProjectSecretStore(secrets), WithExecutionBackend(backend))
	t.Cleanup(reloaded.beginShutdown)
	err = reloaded.StartTest(test.ID)
	var gateErr *ScriptShakeoutGateError
	if !errors.As(err, &gateErr) || gateErr.Report == nil || !strings.Contains(gateErr.Report.Summary, "not replayed") {
		t.Fatalf("post-restart StartTest error = %v, want non-replay ScriptShakeoutGateError", err)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after restart = %d, want no replay", got)
	}
	if err := reloaded.StartTestWithOptions(test.ID, StartTestOptions{OverrideScriptShakeoutError: true}); err != nil {
		t.Fatalf("explicit post-restart override: %v", err)
	}
	select {
	case startedID := <-backend.called:
		if startedID != test.ID {
			t.Fatalf("backend started %q, want %q", startedID, test.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("explicit override did not start the full run")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after explicit override = %d, want no replay", got)
	}
}

func TestRuntimeSecretOverlayCannotBorrowSameNameProviderVersion(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "overlay-version-provenance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	projectID := storage.DefaultProjectID
	secretStore := &versionedShakeoutSecretStore{
		value: "provider-attested-value",
		metadata: secretstore.Metadata{
			ProjectID: projectID, Name: "API_TOKEN", Provider: "versioned-test", UpdatedAt: time.Now().UTC(),
		},
	}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "overlay-version-provenance", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "overlay-version-provenance", execute: func(context.Context) error {
			executions.Add(1)
			return nil
		}}
	})
	runner := NewTestRunner(2, NewHub(), store, WithProjectSecretStore(secretStore))
	t.Cleanup(runner.beginShutdown)
	runner.StoreScript(&dsl.Script{
		ID: "overlay-version-provenance-script", Name: "Overlay version provenance", ProjectID: projectID, Protocol: protocolName, Version: "1.0.0",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "external side effect", Type: "request", Target: "/", Headers: map[string]string{
			"Authorization": "Bearer {{secret.API_TOKEN}}",
		}}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "overlay version provenance", ProjectID: projectID, ScriptID: "overlay-version-provenance-script", VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	runner.mu.Lock()
	managed := runner.tests[test.ID]
	managed.Groups[0].runtimeSecretValues = map[string]string{"API_TOKEN": "different-execution-overlay"}
	managed.groupSpecs[0].SecretResolver = overlayRuntimeSecretResolver(
		managed.Groups[0].runtimeSecretValues,
		runner.secretResolverForProject(projectID),
	)
	runner.mu.Unlock()

	shakeoutTest, err := runner.beginScriptShakeoutIfNeeded(test.ID, StartTestOptions{})
	if err != nil {
		t.Fatalf("beginScriptShakeoutIfNeeded: %v", err)
	}
	report, err := runner.runScriptShakeoutReport(shakeoutTest)
	if err != nil || !report.Passed {
		t.Fatalf("runScriptShakeoutReport = (%#v, %v), want pass", report, err)
	}
	if err := runner.markScriptShakeoutPassed(test.ID, report); err != nil {
		t.Fatalf("markScriptShakeoutPassed: %v", err)
	}
	validated, err := runner.validateScriptShakeoutPassForStart(test.ID, StartTestOptions{}, nil)
	if err != nil || !validated {
		t.Fatalf("live-process overlay proof validation = (%t, %v), want true", validated, err)
	}
	current, _ := runner.GetTest(test.ID)
	if current.StartAuthorization == nil || current.StartAuthorization.ScriptShakeoutPass == nil ||
		current.StartAuthorization.ScriptShakeoutPass.ReusableAfterRestart {
		t.Fatalf("overlay borrowed provider version evidence: %#v", current.StartAuthorization)
	}

	reloaded := NewTestRunner(2, NewHub(), store, WithProjectSecretStore(secretStore))
	t.Cleanup(reloaded.beginShutdown)
	err = reloaded.StartTest(test.ID)
	var gateErr *ScriptShakeoutGateError
	if !errors.As(err, &gateErr) || gateErr.Report == nil || !strings.Contains(gateErr.Report.Summary, "not replayed") {
		t.Fatalf("post-restart overlay StartTest error = %v, want non-replay ScriptShakeoutGateError", err)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("overlay shakeout action executions after restart = %d, want no replay", got)
	}
}

func TestUnversionedShakeoutProofCannotCrossSameProcessLeadershipTerms(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "term-local-shakeout.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	leadership := newExecutionLeadership(store, "term-local-shakeout")
	leadership.tick()
	t.Cleanup(leadership.relinquish)
	firstTerm, err := func() (executionLeaseTerm, error) {
		owner, generation := leadership.executionLeaseTerm()
		if owner == "" || generation == 0 {
			return executionLeaseTerm{}, errNotExecutionLeader
		}
		return executionLeaseTerm{ownerID: owner, generation: generation}, nil
	}()
	if err != nil {
		t.Fatal(err)
	}
	secrets := &runtimeProjectionSecretStore{values: make(map[string]string), attemptStore: store}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "term-local-shakeout", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "term-local-shakeout", execute: func(context.Context) error {
			executions.Add(1)
			return nil
		}}
	})
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner := NewTestRunner(2, NewHub(), store,
		WithExecutionLeadership(leadership), WithProjectSecretStore(secrets), WithExecutionBackend(backend))
	t.Cleanup(runner.beginShutdown)
	runner.StoreScript(&dsl.Script{
		ID: "term-local-shakeout-script", Name: "Term-local shakeout", Protocol: protocolName, Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "external side effect", Type: "request", Target: "/"}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "term-local shakeout", ScriptID: "term-local-shakeout-script", VUsers: 1, Duration: "1m", ScriptShakeout: true,
		RuntimeSettings: &RuntimeSettingsOverride{HTTP: &dsl.HTTPConfig{Auth: dsl.AuthConfig{
			Type: "bearer", Token: "unversioned-term-local-credential",
		}}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	shakeoutTest, err := runner.beginScriptShakeoutIfNeededWithExpectedTerm(test.ID, StartTestOptions{}, &firstTerm)
	if err != nil {
		t.Fatalf("beginScriptShakeoutIfNeededWithExpectedTerm: %v", err)
	}
	report, err := runner.runScriptShakeoutReport(shakeoutTest)
	if err != nil || !report.Passed {
		t.Fatalf("runScriptShakeoutReport = (%#v, %v), want pass", report, err)
	}
	if err := runner.markScriptShakeoutPassedWithExpectedTerm(test.ID, report, &firstTerm); err != nil {
		t.Fatalf("markScriptShakeoutPassedWithExpectedTerm: %v", err)
	}
	validated, err := runner.validateScriptShakeoutPassForStart(test.ID, StartTestOptions{}, &firstTerm)
	if err != nil || !validated {
		t.Fatalf("first-term pass validation = (%t, %v), want true", validated, err)
	}

	leadership.relinquish()
	leadership.tick()
	secondTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatalf("capture reacquired term: %v", err)
	}
	if secondTerm == firstTerm {
		t.Fatalf("leadership term did not rotate: %#v", secondTerm)
	}
	err = runner.StartTest(test.ID)
	var gateErr *ScriptShakeoutGateError
	if !errors.As(err, &gateErr) || gateErr.Report == nil || !strings.Contains(gateErr.Report.Summary, "takeover") {
		t.Fatalf("successor-term StartTest error = %v, want term-local ScriptShakeoutGateError", err)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after same-process takeover = %d, want no replay", got)
	}
	if err := runner.StartTestWithOptions(test.ID, StartTestOptions{OverrideScriptShakeoutError: true}); err != nil {
		t.Fatalf("successor-term explicit override: %v", err)
	}
	select {
	case startedID := <-backend.called:
		if startedID != test.ID {
			t.Fatalf("backend started %q, want %q", startedID, test.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("successor-term explicit override did not start the full run")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after successor override = %d, want no replay", got)
	}
}

func TestQueuedCIRecoveryDistinguishesTransientAndInvalidGitHubCheckState(t *testing.T) {
	for _, tt := range []struct {
		name       string
		checkStore *scriptedGitHubCheckReadStore
		wantStatus string
		wantStale  bool
	}{
		{
			name: "transient read", wantStatus: "QUEUED", wantStale: true,
			checkStore: &scriptedGitHubCheckReadStore{failReads: 1, err: errors.New("temporary check store outage")},
		},
		{
			name: "permanent integrity failure", wantStatus: "FAILED",
			checkStore: &scriptedGitHubCheckReadStore{link: &storage.GitHubCheckLink{
				Kind: "ci", TestID: "placeholder", Owner: "different", Repo: "repository", CommitSHA: "deadbeef", CheckRunID: 42, CheckRunURL: "https://github.test/check/42",
			}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			projectID := "project-ci-check-classification-" + strings.ReplaceAll(tt.name, " ", "-")
			policy := ciRunSourceTestPolicy()
			runner := NewTestRunner(2, NewHub(), nil)
			runner.projectStore = &staticCIProjectReadStore{project: &storage.Project{
				ID: projectID, Name: "CI check classification", UpdatedAt: time.Now().UTC(),
				Settings: storage.ProjectSettings{GitHub: storage.GitHubProjectPolicy{Actions: policy}},
			}}
			storePreflightScript(runner, "ci-check-classification-script-"+tt.name, projectID)
			metadata, err := encodeCIRunMetadata(ciRunMetadata{
				SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"),
				ScriptIdentity: "script_id:ci-check-classification-script-" + tt.name,
				PolicyRevision: policy.Revision, IdempotencySHA: sha256String(tt.name), PublishCheck: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			test, err := runner.CreateTest(CreateTestRequest{
				Name: "CI check classification", ProjectID: projectID, ScriptID: "ci-check-classification-script-" + tt.name,
				VUsers: 1, Duration: "1m", MaterializationKey: metadata,
			})
			if err != nil {
				t.Fatalf("CreateTest: %v", err)
			}
			if tt.checkStore.link != nil {
				tt.checkStore.link.TestID = test.ID
			}
			runner.githubCheckStore = tt.checkStore
			err = runner.recoverQueuedCIRunWithTerm(test.ID, executionLeaseTerm{})
			if errors.Is(err, errStartAdmissionEvidenceStale) != tt.wantStale {
				t.Fatalf("recoverQueuedCIRun error = %v, stale=%t want %t", err, errors.Is(err, errStartAdmissionEvidenceStale), tt.wantStale)
			}
			current, _ := runner.GetTest(test.ID)
			if current.Status != tt.wantStatus {
				t.Fatalf("CI run status = %q, want %q (error %q)", current.Status, tt.wantStatus, current.Error)
			}
			if !tt.wantStale {
				runner.mu.RLock()
				_, retrying := runner.ciRecoveryRetrying[test.ID]
				runner.mu.RUnlock()
				if retrying {
					t.Fatal("permanent GitHub Check integrity error started a recovery retry loop")
				}
			}
			runner.beginShutdown()
		})
	}
}

func TestLeadershipLossCancelsShakeoutBeforeSuccessorRecovery(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "shakeout-leadership-loss.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	started := make(chan struct{})
	exited := make(chan struct{})
	var startedOnce sync.Once
	var exitedOnce sync.Once
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "shakeout-leadership-loss", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "shakeout-leadership-loss", execute: func(ctx context.Context) error {
			executions.Add(1)
			startedOnce.Do(func() { close(started) })
			<-ctx.Done()
			exitedOnce.Do(func() { close(exited) })
			return ctx.Err()
		}}
	})
	leadership := newExecutionLeadership(store, "shakeout-old-leader")
	leadership.tick()
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership))
	runner.StoreScript(&dsl.Script{
		ID: "shakeout-leadership-loss-script", Name: "Shakeout leadership loss", Protocol: protocolName, Version: "1.0.0",
		Actions: []dsl.Action{{Name: "blocking external action", Type: "request", Target: "/"}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "shakeout leadership loss", ScriptID: "shakeout-leadership-loss-script",
		VUsers: 1, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	startDone := make(chan error, 1)
	go func() { startDone <- runner.StartTest(test.ID) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("shakeout action did not start")
	}
	leadership.relinquish()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("old-leader shakeout action ignored epoch cancellation")
	}
	select {
	case err := <-startDone:
		if !errors.Is(err, errNotExecutionLeader) {
			t.Fatalf("old-leader StartTest error = %v, want execution fencing", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old-leader StartTest did not return after lease loss")
	}
	oldView, _ := runner.GetTest(test.ID)
	if oldView.Status == "SHAKEOUT_REVIEW" {
		t.Fatalf("leadership cancellation was misclassified as script failure: %#v", oldView)
	}

	successor := newExecutionLeadership(store, "shakeout-successor")
	successor.tick()
	t.Cleanup(successor.relinquish)
	newRunner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(successor))
	recovered, ok := newRunner.GetTest(test.ID)
	if !ok || recovered.Status != "FAILED" || !strings.Contains(recovered.Error, "ambiguous") || !strings.Contains(recovered.Error, "not replayed") {
		t.Fatalf("successor recovery = %#v, want interrupted SHAKEOUT terminalized", recovered)
	}
	if recovered.StartAuthorization != nil && recovered.StartAuthorization.ScriptShakeoutPass != nil {
		t.Fatalf("ambiguous restart fabricated shakeout pass evidence: %#v", recovered.StartAuthorization)
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout actions after successor recovery = %d, want no duplicate side effect", got)
	}
}

func TestReloadRehydratesPriorEpochPassiveFenceAndReusesCurrentTerm(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "passive-fence-reload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	leadership := newExecutionLeadership(store, "passive-fence-reload")
	leadership.tick()
	t.Cleanup(leadership.relinquish)
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(leadership))
	storePreflightScript(runner, "passive-fence-reload-script", storage.DefaultProjectID)
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "passive fence reload", ScriptID: "passive-fence-reload-script", VUsers: 1, Duration: "1m",
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	oldTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	oldPointer := runner.tests[test.ID]
	if oldPointer.executionFence != "" {
		runner.mu.Unlock()
		t.Fatalf("create-only token leaked onto passive row: %q", oldPointer.executionFence)
	}
	// Simulate a passive row retained by an older controller build/materializer.
	oldPointer.executionFence = oldTerm.ownerID
	oldPointer.executionGeneration = oldTerm.generation
	runner.mu.Unlock()

	leadership.relinquish()
	leadership.tick()
	newTerm, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	if newTerm == oldTerm {
		t.Fatalf("leadership term did not rotate: %#v", newTerm)
	}
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("reloadStoredTests: %v", err)
	}
	runner.mu.RLock()
	rehydrated := runner.tests[test.ID]
	runner.mu.RUnlock()
	if rehydrated == nil {
		t.Fatal("reloaded test is missing")
	}
	if rehydrated == oldPointer || rehydrated.executionFence != "" {
		t.Fatalf("prior-epoch passive runtime was reused: old=%p current=%p fence=%q", oldPointer, rehydrated, rehydrated.executionFence)
	}

	runner.mu.Lock()
	rehydrated.executionFence = newTerm.ownerID
	rehydrated.executionGeneration = newTerm.generation
	runner.mu.Unlock()
	if err := runner.reloadStoredTests(); err != nil {
		t.Fatalf("current-term reloadStoredTests: %v", err)
	}
	runner.mu.RLock()
	reused := runner.tests[test.ID]
	fence := reused.executionFence
	runner.mu.RUnlock()
	if reused != rehydrated || fence != newTerm.ownerID {
		t.Fatalf("current-term passive runtime was not preserved: before=%p after=%p fence=%q", rehydrated, reused, fence)
	}
}

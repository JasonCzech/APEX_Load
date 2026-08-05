package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

type replacingMissingCIProjectStore struct {
	runner      *TestRunner
	testID      string
	replacement *ManagedTest
}

type blockingQueuedCIProjectStore struct {
	project           storage.Project
	err               error
	entered           chan struct{}
	release           chan struct{}
	enteredOnce       sync.Once
	remainingFailures atomic.Int32
	active            atomic.Int32
	maxActive         atomic.Int32
}

func (s *blockingQueuedCIProjectStore) GetProject(id string) (*storage.Project, error) {
	active := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		maximum := s.maxActive.Load()
		if active <= maximum || s.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	s.enteredOnce.Do(func() { close(s.entered) })
	<-s.release
	for {
		remaining := s.remainingFailures.Load()
		if remaining <= 0 {
			break
		}
		if s.remainingFailures.CompareAndSwap(remaining, remaining-1) {
			return nil, s.err
		}
	}
	if normalizeProjectID(id) != normalizeProjectID(s.project.ID) {
		return nil, nil
	}
	project := s.project
	return &project, nil
}

func (s *replacingMissingCIProjectStore) GetProject(string) (*storage.Project, error) {
	s.runner.mu.Lock()
	s.runner.tests[s.testID] = s.replacement
	s.runner.mu.Unlock()
	return nil, sql.ErrNoRows
}

type flakyStartAdmissionRevisionReader struct {
	remaining atomic.Int32
	err       error
	revision  int64
}

func (r *flakyStartAdmissionRevisionReader) StartAdmissionRevision() (int64, error) {
	for {
		remaining := r.remaining.Load()
		if remaining <= 0 {
			return r.revision, nil
		}
		if r.remaining.CompareAndSwap(remaining, remaining-1) {
			return 0, r.err
		}
	}
}

type flakyCITestStore struct {
	storage.Store
	remainingGetFailures atomic.Int32
	err                  error
}

func (s *flakyCITestStore) GetTest(id string) (*storage.TestRecord, error) {
	for {
		remaining := s.remainingGetFailures.Load()
		if remaining <= 0 {
			return s.Store.GetTest(id)
		}
		if s.remainingGetFailures.CompareAndSwap(remaining, remaining-1) {
			return nil, s.err
		}
	}
}

func (s *flakyCITestStore) SaveTestStartIfNoDeploymentLease(test *storage.TestRecord, leaseName, ownerID, tokenHash string) error {
	return s.Store.(interface {
		SaveTestStartIfNoDeploymentLease(*storage.TestRecord, string, string, string) error
	}).SaveTestStartIfNoDeploymentLease(test, leaseName, ownerID, tokenHash)
}

func (s *flakyCITestStore) SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *storage.TestRecord, executionLeaseName, executionOwnerID, deploymentLeaseName, deploymentOwnerID, deploymentTokenHash string) (bool, error) {
	return s.Store.(interface {
		SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(*storage.TestRecord, string, string, string, string, string) (bool, error)
	}).SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test, executionLeaseName, executionOwnerID, deploymentLeaseName, deploymentOwnerID, deploymentTokenHash)
}

func simpleLiveCIRunRequest(t *testing.T, idempotencyKey, protocolName string) *http.Request {
	t.Helper()
	if protocolName == "" {
		protocolName = "http"
	}
	scriptPayload, err := json.Marshal(&dsl.Script{
		Name: "CI transient admission", Version: "1.0.0", Protocol: protocolName,
		Config:  dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: http.MethodGet, Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	publishCheck := false
	payload, err := json.Marshal(ciRunCreateRequest{
		ProjectID: "project-ci", Script: scriptPayload, ScriptPath: "tests/transient-admission.json",
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "1m"},
		Source:    ciTestRunSource("1"), PublishCheck: &publishCheck,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(payload))
	request.Header.Set("Idempotency-Key", idempotencyKey)
	return request
}

func TestLiveCIRunRetriesTransientAdmissionReadsWithoutTerminalizing(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		install func(*TestRunner, *storage.SQLiteStore, error)
	}{
		{
			name: "revision reader",
			install: func(runner *TestRunner, _ *storage.SQLiteStore, outage error) {
				reader := &flakyStartAdmissionRevisionReader{err: outage, revision: 1}
				reader.remaining.Store(1)
				runner.startAdmissionRevisionStore = reader
			},
		},
		{
			name: "capacity binding reader",
			install: func(runner *TestRunner, store *storage.SQLiteStore, outage error) {
				flaky := &flakyCITestStore{Store: store, err: outage}
				flaky.remainingGetFailures.Store(1)
				runner.testStore = flaky
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
			backend := &executionBackendSpy{called: make(chan string, 1)}
			server.runner.setExecutionBackend(backend)
			outage := errors.New("temporary " + testCase.name + " outage")
			server.afterCIRunMaterialize = func(*ManagedTest) {
				testCase.install(server.runner, store, outage)
			}
			recorder := httptest.NewRecorder()
			server.handleCreateCIRun(recorder, simpleLiveCIRunRequest(t, "ci-transient-"+strings.ReplaceAll(testCase.name, " ", "-"), "http"))
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("response = %d, body=%s; want 503", recorder.Code, recorder.Body.String())
			}
			select {
			case testID := <-backend.called:
				current, ok := server.runner.GetTest(testID)
				if !ok || current.Status == "FAILED" || current.Error != "" {
					t.Fatalf("recovered CI run = %#v", current)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("queued CI recovery did not retry after storage recovered")
			}
		})
	}
}

func TestLiveCIRunRetriesStartTransitionWrites(t *testing.T) {
	for _, status := range []string{"INITIALIZING", "SHAKEOUT"} {
		t.Run(status, func(t *testing.T) {
			policy := ciRunSourceTestPolicy()
			protocolName := "http"
			if status == "SHAKEOUT" {
				protocolName = registerRunnerTestProtocol(t, "ci-transition-shakeout", func() protocol.Protocol {
					return &shakeoutActionProtocol{name: "ci-transition-shakeout", execute: func(context.Context) error { return nil }}
				})
				policy.AllowedProtocols = append(policy.AllowedProtocols, protocolName)
			}
			server, store := newCIContractTestServer(t, policy)
			backend := &executionBackendSpy{called: make(chan string, 1)}
			server.runner.setExecutionBackend(backend)
			outage := errors.New("temporary " + status + " persistence outage")
			var persistence *scriptedTestPersistence
			server.afterCIRunMaterialize = func(test *ManagedTest) {
				failCount := startAdmissionPhaseAttempts
				if status == "SHAKEOUT" {
					server.runner.mu.Lock()
					server.runner.tests[test.ID].ScriptShakeout = true
					server.runner.mu.Unlock()
				}
				persistence = &scriptedTestPersistence{
					testPersistence: store, err: outage, failSaveCalls: failCalls(1, failCount),
				}
				server.runner.testStore = persistence
			}
			recorder := httptest.NewRecorder()
			server.handleCreateCIRun(recorder, simpleLiveCIRunRequest(t, "ci-transition-"+status, protocolName))
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("response = %d, body=%s; save calls=%d; want 503", recorder.Code, recorder.Body.String(), persistence.saveCalls)
			}
			select {
			case testID := <-backend.called:
				current, ok := server.runner.GetTest(testID)
				if !ok || current.Status == "FAILED" || current.Error != "" {
					t.Fatalf("recovered CI run = %#v", current)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("queued CI recovery did not retry the start transition write")
			}
		})
	}
}

func TestLiveCIRunDeletionBeforeStartEvictsGhostAndReturnsNotFound(t *testing.T) {
	server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
	var testID string
	server.afterCIRunMaterialize = func(test *ManagedTest) {
		testID = test.ID
		if err := store.DeleteTest(test.ID); err != nil {
			t.Fatalf("DeleteTest: %v", err)
		}
	}
	recorder := httptest.NewRecorder()
	server.handleCreateCIRun(recorder, simpleLiveCIRunRequest(t, "ci-deleted-before-start", "http"))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("response = %d, body=%s; want 404", recorder.Code, recorder.Body.String())
	}
	if testID == "" {
		t.Fatal("CI run was not materialized")
	}
	if ghost, ok := server.runner.GetTest(testID); ok || ghost != nil {
		t.Fatalf("deleted durable CI run remained process-local: %#v", ghost)
	}
}

func TestMissingCIProjectReadCannotEvictConcurrentReplacementIdentity(t *testing.T) {
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion:  ciRunSchemaVersion,
		Source:         ciTestRunSource("1"),
		PolicyRevision: ciRunSourceTestPolicy().Revision,
		IdempotencySHA: sha256String("missing-project-replacement"),
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(2, NewHub(), nil)
	old := &ManagedTest{
		ID: "missing-project-ci", Name: "old CI identity", Status: "QUEUED", ProjectID: "project-ci-recreated",
		CreatedAt: time.Now().UTC().Add(-time.Minute), MaterializationKey: metadata,
	}
	replacement := &ManagedTest{
		ID: old.ID, Name: "replacement CI identity", Status: "QUEUED", ProjectID: old.ProjectID,
		CreatedAt: time.Now().UTC(), MaterializationKey: metadata,
	}
	runner.mu.Lock()
	runner.tests[old.ID] = old
	runner.mu.Unlock()
	runner.projectStore = &replacingMissingCIProjectStore{runner: runner, testID: old.ID, replacement: replacement}

	err = runner.validateCIRunPolicyForStart(old.ID)
	if !errors.Is(err, errStoredTestMissing) {
		t.Fatalf("validateCIRunPolicyForStart error = %v, want missing durable identity", err)
	}
	runner.mu.RLock()
	retained := runner.tests[old.ID]
	runner.mu.RUnlock()
	if retained != replacement {
		t.Fatalf("concurrent replacement identity = %#v, want exact replacement pointer preserved", retained)
	}
}

func TestLiveCIRunRetriesCompletedShakeoutOutcomeWithoutAdoptionOrReplay(t *testing.T) {
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "ci-durable-shakeout", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "ci-durable-shakeout", execute: func(context.Context) error {
			executions.Add(1)
			return nil
		}}
	})
	policy := ciRunSourceTestPolicy()
	policy.AllowedProtocols = append(policy.AllowedProtocols, protocolName)
	server, store := newCIContractTestServer(t, policy)
	backend := &executionBackendSpy{called: make(chan string, 1)}
	server.runner.setExecutionBackend(backend)
	persistenceErr := errors.New("temporary CI outcome persistence outage")
	var scripted *scriptedTestPersistence
	server.afterCIRunMaterialize = func(test *ManagedTest) {
		server.runner.mu.Lock()
		server.runner.tests[test.ID].ScriptShakeout = true
		server.runner.mu.Unlock()
		scripted = &scriptedTestPersistence{
			testPersistence: store,
			err:             persistenceErr,
			failSaveCalls:   failCalls(2, durableTransitionAttempts),
		}
		server.runner.testStore = scripted
	}

	scriptPayload, err := json.Marshal(&dsl.Script{
		Name: "CI durable shakeout", Version: "1.0.0", Protocol: protocolName,
		Config:  dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{Name: "external side effect", Type: "request", Method: http.MethodGet, Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	publishCheck := false
	payload, err := json.Marshal(ciRunCreateRequest{
		ProjectID: "project-ci", Script: scriptPayload, ScriptPath: "tests/durable-shakeout.json",
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "1m"},
		Source:    ciTestRunSource("1"), PublishCheck: &publishCheck,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(payload))
	request.Header.Set("Idempotency-Key", "ci-durable-shakeout-outcome-retry")
	recorder := httptest.NewRecorder()
	server.handleCreateCIRun(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("live CI response = %d, body=%s; want 503 recovery response", recorder.Code, recorder.Body.String())
	}
	if scripted == nil {
		t.Fatal("scripted persistence was not installed")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after failed outcome write = %d, want one", got)
	}

	select {
	case testID := <-backend.called:
		_ = waitForTestStatusWithin(t, server.runner, testID, 2*time.Second, "INITIALIZING")
		server.runner.mu.RLock()
		pendingOutcome := server.runner.tests[testID].pendingScriptShakeoutOutcome
		server.runner.mu.RUnlock()
		if pendingOutcome != nil {
			t.Fatalf("recovered CI run retained outcome: %#v", pendingOutcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued CI recovery did not finish the retained outcome and launch the run")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after recovery = %d, want no replay", got)
	}
}

func TestLiveCIRunRetriesCorrectablePostShakeoutSecretGateWithoutReplay(t *testing.T) {
	secretStore := &versionedShakeoutSecretStore{
		value: "available-during-shakeout",
		metadata: secretstore.Metadata{
			ProjectID: "project-ci", Name: "SESSION_TOKEN", Provider: "versioned-test", UpdatedAt: time.Now().UTC(),
		},
	}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "ci-post-shakeout-secret", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "ci-post-shakeout-secret", execute: func(context.Context) error {
			executions.Add(1)
			secretStore.mu.Lock()
			secretStore.value = ""
			secretStore.mu.Unlock()
			return nil
		}}
	})
	policy := ciRunSourceTestPolicy()
	policy.AllowedProtocols = append(policy.AllowedProtocols, protocolName)
	server, _ := newCIContractTestServer(t, policy)
	server.runner.projectSecretStore = secretStore
	backend := &executionBackendSpy{called: make(chan string, 1)}
	server.runner.setExecutionBackend(backend)
	server.afterCIRunMaterialize = func(test *ManagedTest) {
		server.runner.mu.Lock()
		server.runner.tests[test.ID].ScriptShakeout = true
		server.runner.mu.Unlock()
	}

	scriptPayload, err := json.Marshal(&dsl.Script{
		Name: "CI secret gate", Version: "1.0.0", Protocol: protocolName, ProjectID: "project-ci",
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "authorized side effect", Type: "request", Method: http.MethodGet, Target: "/health",
			Headers: map[string]string{"Authorization": "Bearer {{secret.SESSION_TOKEN}}"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	publishCheck := false
	payload, err := json.Marshal(ciRunCreateRequest{
		ProjectID: "project-ci", Script: scriptPayload, ScriptPath: "tests/secret-gate.json",
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "1m"},
		Source:    ciTestRunSource("1"), PublishCheck: &publishCheck,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(payload))
	request.Header.Set("Idempotency-Key", "ci-post-shakeout-secret-gate")
	recorder := httptest.NewRecorder()
	server.handleCreateCIRun(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("live CI response = %d, body=%s; want 503 retryable secret gate", recorder.Code, recorder.Body.String())
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions at secret gate = %d, want one", got)
	}
	secretStore.mu.Lock()
	secretStore.value = "available-during-shakeout"
	secretStore.mu.Unlock()
	select {
	case testID := <-backend.called:
		_ = waitForTestStatusWithin(t, server.runner, testID, 2*time.Second, "INITIALIZING")
	case <-time.After(5 * time.Second):
		t.Fatal("CI recovery did not launch after the correctable secret was restored")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after secret recovery = %d, want no replay", got)
	}
}

func TestLiveCIRunSurfacesLeadershipLossAfterEnteringShakeout(t *testing.T) {
	entered := make(chan struct{})
	var enteredOnce atomic.Bool
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "ci-shakeout-lease-loss", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "ci-shakeout-lease-loss", execute: func(ctx context.Context) error {
			executions.Add(1)
			if enteredOnce.CompareAndSwap(false, true) {
				close(entered)
			}
			<-ctx.Done()
			return ctx.Err()
		}}
	})
	policy := ciRunSourceTestPolicy()
	policy.AllowedProtocols = append(policy.AllowedProtocols, protocolName)
	server, store := newCIContractTestServer(t, policy)
	leadership := newExecutionLeadership(store, "ci-live-shakeout-loss")
	leadership.tick()
	server.runner.leadership = leadership
	server.runner.durableExecutionFence = true
	server.afterCIRunMaterialize = func(test *ManagedTest) {
		server.runner.mu.Lock()
		server.runner.tests[test.ID].ScriptShakeout = true
		server.runner.mu.Unlock()
	}

	scriptPayload, err := json.Marshal(&dsl.Script{
		Name: "CI shakeout lease loss", Version: "1.0.0", Protocol: protocolName,
		Config:  dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{Name: "blocking side effect", Type: "request", Method: http.MethodGet, Target: "/health"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	publishCheck := false
	payload, err := json.Marshal(ciRunCreateRequest{
		ProjectID: "project-ci", Script: scriptPayload, ScriptPath: "tests/lease-loss.json",
		Execution: ciRunExecutionRequest{VUsers: 1, Duration: "1m"}, Source: ciTestRunSource("1"), PublishCheck: &publishCheck,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ci/runs", bytes.NewReader(payload))
	request.Header.Set("Idempotency-Key", "ci-live-shakeout-lease-loss")
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		server.handleCreateCIRun(recorder, request)
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("live CI handler did not enter script shakeout")
	}
	leadership.relinquish()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("live CI handler did not return after lease loss")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("lease-loss response = %d, body=%s; want 503", recorder.Code, recorder.Body.String())
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after lease loss = %d, want no adoption/replay", got)
	}
}

func TestQueuedCIShakeoutReviewIsTerminalizedAfterRestartWithoutReplay(t *testing.T) {
	policy := ciRunSourceTestPolicy()
	server, store := newCIContractTestServer(t, policy)
	script, err := server.runner.StoreScriptDurable(&dsl.Script{
		ID: "ci-review-restart-script", Name: "CI review restart", Version: "1.0.0", Protocol: "http",
		ProjectID: "project-ci", Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{Name: "must not replay", Type: "request", Method: http.MethodGet, Target: "/health"}},
	})
	if err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"), ScriptIdentity: "script_id:" + script.ID,
		PolicyRevision: policy.Revision, IdempotencySHA: sha256String("ci-review-restart"), PublishCheck: false,
	})
	if err != nil {
		t.Fatalf("encodeCIRunMetadata: %v", err)
	}
	created, err := server.runner.CreateTest(CreateTestRequest{
		Name: "CI review restart", ProjectID: "project-ci", ScriptID: script.ID,
		VUsers: 1, Duration: "1m", ScriptShakeout: true, MaterializationKey: metadata,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	const retainedReview = "script shakeout rejected the authored dependency contract"
	server.runner.mu.Lock()
	managed := server.runner.tests[created.ID]
	managed.Status = "SHAKEOUT_REVIEW"
	managed.Error = retainedReview
	record := buildManagedTestRecord(managed)
	server.runner.mu.Unlock()
	if err := store.SaveTest(record); err != nil {
		t.Fatalf("persist SHAKEOUT_REVIEW crash window: %v", err)
	}

	backend := &executionBackendSpy{called: make(chan string, 1)}
	recovered := NewTestRunner(2, NewHub(), store, WithExecutionBackend(backend))
	deadline := time.Now().Add(5 * time.Second)
	for {
		durable, getErr := store.GetTest(created.ID)
		if getErr != nil {
			t.Fatalf("GetTest: %v", getErr)
		}
		if durable.Status == "FAILED" {
			if !strings.Contains(durable.Error, retainedReview) {
				t.Fatalf("recovered failure omitted retained review evidence: %q", durable.Error)
			}
			local, ok := recovered.GetTest(created.ID)
			if !ok || local.Status != "FAILED" {
				t.Fatalf("recovered local status = %#v, want FAILED", local)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable CI review remained %q after restart", durable.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case startedID := <-backend.called:
		t.Fatalf("CI review recovery replayed or launched %q", startedID)
	default:
	}
}

func TestQueuedCIRecoveryScanIsBoundedAndCoordinatorSerializesSharedOutage(t *testing.T) {
	const runCount = 24
	policy := ciRunSourceTestPolicy()
	backend := &executionBackendSpy{called: make(chan string, runCount)}
	runner := NewTestRunner(runCount+2, NewHub(), nil, WithExecutionBackend(backend))
	runner.StoreScript(&dsl.Script{
		ID: "queued-ci-bounded-recovery-script", Name: "Queued CI bounded recovery", Version: "1.0.0", Protocol: "http",
		ProjectID: "project-ci", Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{Name: "health", Type: "request", Method: http.MethodGet, Target: "/health"}},
	})
	metadata, err := encodeCIRunMetadata(ciRunMetadata{
		SchemaVersion: ciRunSchemaVersion, Source: ciTestRunSource("1"),
		ScriptIdentity: "script_id:queued-ci-bounded-recovery-script", PolicyRevision: policy.Revision,
		IdempotencySHA: sha256String("queued-ci-bounded-recovery"),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := make(map[string]struct{}, runCount)
	for index := 0; index < runCount; index++ {
		created, createErr := runner.CreateTest(CreateTestRequest{
			Name: fmt.Sprintf("queued CI recovery %02d", index), ProjectID: "project-ci",
			ScriptID: "queued-ci-bounded-recovery-script", VUsers: 1, Duration: "1m",
		})
		if createErr != nil {
			t.Fatalf("CreateTest(%d): %v", index, createErr)
		}
		runner.mu.Lock()
		runner.tests[created.ID].MaterializationKey = metadata
		runner.mu.Unlock()
		wantIDs[created.ID] = struct{}{}
	}

	outage := errors.New("shared project policy provider outage")
	projectStore := &blockingQueuedCIProjectStore{
		project: storage.Project{
			ID: "project-ci", Name: "CI recovery", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			Settings: storage.ProjectSettings{GitHub: storage.GitHubProjectPolicy{Actions: policy}},
		},
		err: outage, entered: make(chan struct{}), release: make(chan struct{}),
	}
	projectStore.remainingFailures.Store(5)
	runner.projectStore = projectStore
	t.Cleanup(func() {
		select {
		case <-projectStore.release:
		default:
			close(projectStore.release)
		}
	})

	recoveryDone := make(chan error, 1)
	go func() { recoveryDone <- runner.recoverQueuedCIRuns() }()
	select {
	case recoveryErr := <-recoveryDone:
		if recoveryErr != nil {
			t.Fatalf("recoverQueuedCIRuns: %v", recoveryErr)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("queued CI recovery scan waited for a row-level provider call")
	}
	select {
	case <-projectStore.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("bounded queued CI coordinator did not begin recovery")
	}
	// Keep the shared read blocked briefly. A per-row retry implementation would
	// accumulate one active provider call per row; the coordinator permits one.
	time.Sleep(75 * time.Millisecond)
	if got := projectStore.maxActive.Load(); got != 1 {
		t.Fatalf("concurrent queued CI provider reads = %d, want one bounded worker", got)
	}
	close(projectStore.release)

	started := make(map[string]struct{}, runCount)
	deadline := time.After(10 * time.Second)
	for len(started) < runCount {
		select {
		case id := <-backend.called:
			if _, wanted := wantIDs[id]; !wanted {
				t.Fatalf("unexpected recovered test %q", id)
			}
			if _, duplicate := started[id]; duplicate {
				t.Fatalf("queued CI test %q launched more than once", id)
			}
			started[id] = struct{}{}
		case <-deadline:
			t.Fatalf("queued CI coordinator recovered %d/%d rows", len(started), runCount)
		}
	}
	if got := projectStore.maxActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent queued CI provider reads = %d, want one", got)
	}
	for limit := time.Now().Add(2 * time.Second); ; {
		runner.mu.RLock()
		remaining := len(runner.ciRecoveryRetrying)
		workerUp := runner.ciRecoveryRetryWorkerUp
		runner.mu.RUnlock()
		if remaining == 0 && !workerUp {
			break
		}
		if time.Now().After(limit) {
			t.Fatalf("queued CI coordinator did not drain: remaining=%d worker_up=%t", remaining, workerUp)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestQueuedCIRecoveryEmptyHandoffCannotLoseConcurrentEnqueue(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	var hookCalls atomic.Int32
	runner.beforeQueuedCIRecoveryEmptyRelease = func() {
		if hookCalls.Add(1) == 1 {
			close(entered)
			<-release
		}
	}
	term := executionLeaseTerm{}
	runner.mu.Lock()
	runner.tests["concurrent-enqueue"] = &ManagedTest{ID: "concurrent-enqueue", Status: "QUEUED", CreatedAt: time.Now().UTC()}
	runner.tests["ineligible-seed"] = &ManagedTest{ID: "ineligible-seed", Status: "COMPLETE", CreatedAt: time.Now().UTC()}
	runner.mu.Unlock()
	// An ineligible durable identity makes the worker remove this seed at its first scan and
	// enters the exact empty-queue ownership handoff.
	runner.enqueueQueuedCIRecoveryRetryWithTerm("ineligible-seed", term)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not enter empty-queue handoff")
	}
	enqueued := make(chan struct{})
	go func() {
		runner.enqueueQueuedCIRecoveryRetryWithTerm("concurrent-enqueue", term)
		close(enqueued)
	}()
	select {
	case <-enqueued:
		t.Fatal("concurrent enqueue bypassed the protected worker handoff")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-enqueued:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent enqueue remained blocked after worker handoff")
	}
	for limit := time.Now().Add(2 * time.Second); ; {
		runner.mu.RLock()
		_, queued := runner.ciRecoveryRetrying["concurrent-enqueue"]
		workerUp := runner.ciRecoveryRetryWorkerUp
		runner.mu.RUnlock()
		if !queued && !workerUp {
			break
		}
		if time.Now().After(limit) {
			t.Fatalf("concurrent enqueue was stranded: queued=%t worker_up=%t", queued, workerUp)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManualStartRecoversPostShakeoutVerificationOutageWithoutSecondRequest(t *testing.T) {
	providerErr := errors.New("temporary versioned secret provider outage")
	secretStore := &versionedShakeoutSecretStore{
		value: "stable-secret",
		metadata: secretstore.Metadata{
			ProjectID: storage.DefaultProjectID, Name: "SESSION_TOKEN", Provider: "versioned-test", UpdatedAt: time.Now().UTC(),
		},
	}
	var executions atomic.Int32
	protocolName := registerRunnerTestProtocol(t, "manual-post-shakeout-recovery", func() protocol.Protocol {
		return &shakeoutActionProtocol{name: "manual-post-shakeout-recovery", execute: func(context.Context) error {
			executions.Add(1)
			secretStore.mu.Lock()
			secretStore.err = providerErr
			secretStore.mu.Unlock()
			return nil
		}}
	})
	backend := &executionBackendSpy{called: make(chan string, 1)}
	runner := NewTestRunner(2, NewHub(), nil, WithProjectSecretStore(secretStore), WithExecutionBackend(backend))
	settings := runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10_000
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings: %v", err)
	}
	runner.StoreScript(&dsl.Script{
		ID: "manual-post-shakeout-recovery-script", Name: "Manual post-shakeout recovery", Version: "1.0.0",
		Protocol: protocolName, Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "one shot", Type: "request", Method: http.MethodGet, Target: "/health",
			Headers: map[string]string{"Authorization": "Bearer {{secret.SESSION_TOKEN}}"},
		}},
	})
	test, err := runner.CreateTest(CreateTestRequest{
		Name: "manual post-shakeout recovery", ScriptID: "manual-post-shakeout-recovery-script",
		VUsers: 15_000, Duration: "1m", ScriptShakeout: true,
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	server := &Server{runner: runner}
	requestBody, err := json.Marshal(StartTestOptions{OverrideCapacityShortfall: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/start", bytes.NewReader(requestBody))
	request.SetPathValue("id", test.ID)
	recorder := httptest.NewRecorder()
	server.handleStartTest(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("manual start response = %d, body=%s; want 503", recorder.Code, recorder.Body.String())
	}
	if retryAfter := recorder.Header().Get("Retry-After"); retryAfter == "" {
		t.Fatal("retryable manual start response omitted Retry-After")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions before recovery = %d, want one", got)
	}
	runner.mu.RLock()
	retainedAuthorization := cloneQueuedStartAuthorization(runner.tests[test.ID].StartAuthorization)
	runner.mu.RUnlock()
	if retainedAuthorization == nil || retainedAuthorization.CapacityShortfall == nil || retainedAuthorization.CapacityShortfall.TotalVUsers != 15_000 {
		t.Fatalf("SHAKEOUT did not retain the accepted capacity override: %#v", retainedAuthorization)
	}
	secretStore.mu.Lock()
	secretStore.err = nil
	secretStore.mu.Unlock()
	select {
	case startedID := <-backend.called:
		if startedID != test.ID {
			t.Fatalf("backend started %q, want %q", startedID, test.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manual verification-pending start did not recover after provider restoration")
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("shakeout action executions after automatic recovery = %d, want no replay", got)
	}
	runner.mu.RLock()
	current := runner.tests[test.ID]
	retainedOutcome := current.pendingScriptShakeoutOutcome
	runner.mu.RUnlock()
	if retainedOutcome != nil {
		t.Fatalf("manual recovery retained completed shakeout outcome: %#v", retainedOutcome)
	}
}

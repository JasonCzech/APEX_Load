package api

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The backend barrier exercises the real public Start/durable-admission path;
// it performs real archive persistence after release, without executing a SUT.
type telemetryPolicyBarrierBackend struct {
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
	done     chan error
	expected atomic.Bool
}

func newTelemetryPolicyBarrierBackend(t *testing.T) *telemetryPolicyBarrierBackend {
	t.Helper()
	backend := &telemetryPolicyBarrierBackend{entered: make(chan struct{}), release: make(chan struct{}), done: make(chan error, 1)}
	t.Cleanup(func() {
		backend.once.Do(func() { close(backend.release) })
		if backend.expected.Load() {
			select {
			case err := <-backend.done:
				if err != nil {
					t.Errorf("owned backend cleanup: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Error("owned backend cleanup did not drain")
			}
		}
	})
	return backend
}

func (backend *telemetryPolicyBarrierBackend) Run(runner *TestRunner, test *ManagedTest) {
	close(backend.entered)
	<-backend.release
	artifacts, err := newRunArtifacts(runner, test, runner.saveRunArchiveRecord)
	if err == nil {
		_, err = artifacts.recorder.Finalize("ABORTED", time.Now().UTC(), "controlled admission fixture", false, false, nil)
	}
	backend.done <- err
	close(backend.done)
}

func waitTelemetryBackend(t *testing.T, backend *telemetryPolicyBarrierBackend) {
	t.Helper()
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("backend did not reach barrier")
	}
}

func finishTelemetryBackend(t *testing.T, backend *telemetryPolicyBarrierBackend) {
	t.Helper()
	backend.once.Do(func() { close(backend.release) })
	select {
	case err := <-backend.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("owned backend did not drain")
	}
}

func setTelemetryPolicyFixtureSettings(t *testing.T, runner *TestRunner, budget float64, interval int) {
	t.Helper()
	settings := runner.GetSystemSettings()
	settings.Thresholds.MaxTelemetrySampleAgeMs = float64Setting(budget)
	settings.General.MetricsIntervalSeconds = interval
	if _, err := runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
}

func TestTelemetryBudgetPolicyStartAdmissionPrecedesBackendAndReopens(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "telemetry-admitted")
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	setTelemetryPolicyFixtureSettings(t, runner, 11, 3)
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatal(err)
	}
	backend.expected.Store(true)
	waitTelemetryBackend(t, backend)
	// Start returned, but the backend has not constructed artifacts or engines.
	// The complete policy/configuration must already be durable at this point.
	stored, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := decodeStoredInitialProvenance(stored.InitialProvenance)
	if stored.Status != "INITIALIZING" || initial == nil || !validTelemetryBudgetPolicy(initial.TelemetryPolicy) || initial.TelemetryPolicy.Budgets[0].LimitMs != 11 || initial.TelemetryPolicy.ExpectedControllerPublicationIntervalMs != 3000 {
		t.Fatalf("Start returned before immutable policy committed: %#v %#v", stored, initial)
	}
	if len(initial.Groups) != 1 || initial.Groups[0].ScriptID != "telemetry-admitted-script" || initial.TotalVUsers != 1 || initial.Duration != "1m" {
		t.Fatalf("policy-only snapshot suppressed complete configuration: %#v", initial)
	}
	hash := initial.TelemetryPolicy.Hash
	setTelemetryPolicyFixtureSettings(t, runner, 999, 1)
	finishTelemetryBackend(t, backend)
	evidence, limits := runner.runTelemetryPolicy(test)
	if evidence.Policy == nil || evidence.Policy.Hash != hash || *limits.MaxTelemetrySampleAgeMs != 11 {
		t.Fatal("asynchronous backend captured settings changed after Start")
	}
	// This is an actual SQLite close/reopen, rather than merely another reader
	// on an open handle. It qualifies durable metadata, not full HA recovery.
	info := store.BackendInfo()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.NewSQLiteStore(info.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	durable, err := reopened.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovered := decodeStoredInitialProvenance(durable.InitialProvenance)
	if recovered == nil || recovered.TelemetryPolicy.Hash != hash || len(recovered.Groups) != 1 {
		t.Fatal("actual close/reopen lost admitted policy/configuration")
	}
	replacement := NewTestRunner(2, NewHub(), reopened)
	manifest, err := replacement.GetTestManifest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TelemetryPolicy == nil || manifest.TelemetryPolicy.Policy == nil || manifest.TelemetryPolicy.Policy.Hash != hash {
		t.Fatal("reopened controller projection changed admitted receipt")
	}
}

// Failure injection only replaces the real durable Start write boundary; all
// other configuration, admission, state and policy reads use the actual store.
type telemetryPolicyFailStartStore struct {
	storage.Store
	target   *storage.SQLiteStore
	failures atomic.Int64
}

var errTelemetryPolicyStartFixture = errors.New("controlled initializing persistence failure")

func (store *telemetryPolicyFailStartStore) SaveTestStartIfNoDeploymentLease(test *storage.TestRecord, name, owner, token string) error {
	if store.failures.Load() > 0 {
		return errTelemetryPolicyStartFixture
	}
	return store.target.SaveTestStartIfNoDeploymentLease(test, name, owner, token)
}

func (store *telemetryPolicyFailStartStore) SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test *storage.TestRecord, executionName, executionOwner, deploymentName, deploymentOwner, token string) (bool, error) {
	if store.failures.Load() > 0 {
		return false, errTelemetryPolicyStartFixture
	}
	return store.target.SaveTestStartIfExecutionLeaseHeldAndNoDeploymentLease(test, executionName, executionOwner, deploymentName, deploymentOwner, token)
}

func TestTelemetryBudgetPolicyFailedAdmissionRecapturesSuccessfulSettings(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending-%t", pending), func(t *testing.T) {
			t.Setenv(config.EnvArchiveDir, t.TempDir())
			runner, store, test := newPassiveTransitionTestRunner(t, fmt.Sprintf("telemetry-retry-%t", pending))
			backend := newTelemetryPolicyBarrierBackend(t)
			runner.setExecutionBackend(backend)
			setTelemetryPolicyFixtureSettings(t, runner, 11, 3)
			if pending {
				runner.mu.Lock()
				test.Status = "PENDING"
				now := time.Now().UTC()
				test.PendingSince = &now
				test.PendingReason = "controlled retry"
				err := runner.saveManagedTestRecord(test)
				runner.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			wrapped := &telemetryPolicyFailStartStore{Store: store, target: store}
			wrapped.failures.Store(100)
			runner.testStore = wrapped
			start := func() error {
				if pending {
					term, err := runner.captureExecutionLeaseTerm()
					if err != nil {
						return err
					}
					return runner.promotePendingTestsWithTerm(term)
				}
				return runner.StartTest(test.ID)
			}
			if err := start(); !errors.Is(err, errTelemetryPolicyStartFixture) {
				t.Fatalf("failure injection did not reach initializing write: %v", err)
			}
			runner.mu.RLock()
			failedInitial := test.initialArchiveProvenance
			status := test.Status
			runner.mu.RUnlock()
			expected := "QUEUED"
			if pending {
				expected = "PENDING"
			}
			if failedInitial != nil || status != expected {
				t.Fatalf("failed admission retained policy/status: %#v %s", failedInitial, status)
			}
			stored, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			if decodeStoredInitialProvenance(stored.InitialProvenance) != nil {
				t.Fatal("failed admission committed an initial receipt")
			}
			setTelemetryPolicyFixtureSettings(t, runner, 999, 1)
			wrapped.failures.Store(0)
			if err := start(); err != nil {
				t.Fatal(err)
			}
			backend.expected.Store(true)
			waitTelemetryBackend(t, backend)
			evidence, _ := runner.runTelemetryPolicy(test)
			if evidence.Policy == nil || evidence.Policy.Budgets[0].LimitMs != 999 || evidence.Policy.ExpectedControllerPublicationIntervalMs != 1000 {
				t.Fatal("successful retry reused failed admission settings")
			}
			finishTelemetryBackend(t, backend)
		})
	}
}

func TestTelemetryBudgetPolicyFailedMarkDoesNotInstallReceipt(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	test := &ManagedTest{ID: "unhydrated", Status: "QUEUED", runtimeHydrationError: "controlled missing runtime"}
	runner.mu.Lock()
	err := runner.markTestInitializingLockedWithExpectedTerm(test, nil)
	runner.mu.Unlock()
	if err == nil || test.initialArchiveProvenance != nil || test.Status != "QUEUED" {
		t.Fatal("failed hydration admission installed an initial receipt")
	}
}

// A cache miss must adopt a durable write-once winner before Start returns,
// rather than waiting for the asynchronous artifact backend to reconcile it.
func TestTelemetryBudgetPolicyAdmissionAdoptsDurableWinnerBeforeBackend(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy-%t", legacy), func(t *testing.T) {
			t.Setenv(config.EnvArchiveDir, t.TempDir())
			runner, store, test := newPassiveTransitionTestRunner(t, fmt.Sprintf("telemetry-winner-%t", legacy))
			backend := newTelemetryPolicyBarrierBackend(t)
			runner.setExecutionBackend(backend)
			setTelemetryPolicyFixtureSettings(t, runner, 11, 3)
			runner.mu.Lock()
			initial, err := runner.telemetryAdmissionProvenanceLocked(test)
			runner.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				initial.TelemetryPolicy = nil
			}
			if saved, err := store.SaveTestInitialProvenance(test.ID, test.ProjectID, initialProvenanceForStorage(initial)); err != nil || !saved {
				t.Fatalf("persist initial winner: %t %v", saved, err)
			}
			setTelemetryPolicyFixtureSettings(t, runner, 999, 1)
			if err := runner.StartTest(test.ID); err != nil {
				t.Fatal(err)
			}
			backend.expected.Store(true)
			waitTelemetryBackend(t, backend)
			evidence, limits := runner.runTelemetryPolicy(test)
			manifest, err := runner.GetTestManifest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			if *limits.MaxTelemetrySampleAgeMs != 11 {
				t.Fatal("Start published losing candidate limits")
			}
			if legacy {
				if evidence.Availability != "unavailable" || evidence.Policy != nil || manifest.TelemetryPolicy.Policy != nil {
					t.Fatal("Start invented a policy for a durable legacy receipt")
				}
			} else if evidence.Policy == nil || evidence.Policy.Hash != initial.TelemetryPolicy.Hash || manifest.TelemetryPolicy.Policy == nil || manifest.TelemetryPolicy.Policy.Hash != initial.TelemetryPolicy.Hash {
				t.Fatal("Start exposed a losing policy before backend reconciliation")
			}
			finishTelemetryBackend(t, backend)
		})
	}
}

func TestTelemetryBudgetPolicyConcurrentManifestNeverPublishesCandidate(t *testing.T) {
	for _, prior := range []string{"none", "current", "legacy"} {
		t.Run(prior, func(t *testing.T) {
			t.Setenv(config.EnvArchiveDir, t.TempDir())
			runner, store, test := newPassiveTransitionTestRunner(t, "telemetry-concurrent-"+prior)
			backend := newTelemetryPolicyBarrierBackend(t)
			runner.setExecutionBackend(backend)
			setTelemetryPolicyFixtureSettings(t, runner, 11, 3)
			runner.mu.Lock()
			initial, err := runner.telemetryAdmissionProvenanceLocked(test)
			runner.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if prior == "legacy" {
				initial.TelemetryPolicy = nil
			}
			if prior != "none" {
				if saved, err := store.SaveTestInitialProvenance(test.ID, test.ProjectID, initialProvenanceForStorage(initial)); err != nil || !saved {
					t.Fatalf("prior receipt: %t %v", saved, err)
				}
			}
			setTelemetryPolicyFixtureSettings(t, runner, 999, 1)
			wrapped := &blockingStartCommitStore{Store: store, target: store, runner: runner}
			entered, release := wrapped.blockNextStartCommit()
			runner.testStore = wrapped
			startDone := make(chan error, 1)
			var releaseOnce sync.Once
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(release) })
				select {
				case <-startDone:
				case <-time.After(10 * time.Second):
					t.Error("owned blocked Start did not drain")
				}
			})
			go func() {
				err := runner.StartTest(test.ID)
				if err == nil {
					backend.expected.Store(true)
				}
				startDone <- err
				close(startDone)
			}()
			select {
			case <-entered:
			case err := <-startDone:
				t.Fatalf("Start did not reach commit barrier: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("Start commit barrier not reached")
			}
			manifest, err := runner.GetTestManifest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			if prior == "current" {
				if manifest.TelemetryPolicy.Policy == nil || manifest.TelemetryPolicy.Policy.Hash != initial.TelemetryPolicy.Hash {
					t.Fatal("concurrent manifest exposed private candidate instead of durable winner")
				}
			} else if manifest.TelemetryPolicy.Availability != "unavailable" || manifest.TelemetryPolicy.Policy != nil {
				t.Fatal("concurrent manifest exposed uncommitted policy")
			}
			if prior == "none" && manifest.InitialConfigurationProvenance.Immutable {
				t.Fatal("concurrent manifest labeled an uncommitted configuration immutable")
			}
			select {
			case <-backend.entered:
				t.Fatal("backend dispatched before commit")
			default:
			}
			releaseOnce.Do(func() { close(release) })
			if err := <-startDone; err != nil {
				t.Fatal(err)
			}
			waitTelemetryBackend(t, backend)
			finishTelemetryBackend(t, backend)
		})
	}
}

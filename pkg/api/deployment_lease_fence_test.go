package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// withUnfencedDeploymentLeaseStartForFaultInjection exists only in test builds.
// Production code has no option capable of enabling this seam.
func withUnfencedDeploymentLeaseStartForFaultInjection() TestRunnerOption {
	return func(runner *TestRunner) {
		runner.testOnlyAllowUnfencedDeploymentLeaseStart = true
	}
}

type unsupportedDeploymentLeaseStartStore struct {
	testPersistence
	saveCalls int
}

func (s *unsupportedDeploymentLeaseStartStore) SaveTest(*storage.TestRecord) error {
	s.saveCalls++
	return nil
}

func TestDeploymentLeaseStartFenceFailsClosedForUnsupportedDurableStore(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	unsupported := &unsupportedDeploymentLeaseStartStore{}
	runner.testStore = unsupported
	err := runner.saveManagedTestStartRecord(&ManagedTest{ID: "start", Status: "INITIALIZING"})
	if err == nil || !strings.Contains(err.Error(), "deployment lease start fence is unavailable") {
		t.Fatalf("saveManagedTestStartRecord error = %v, want missing-capability failure", err)
	}
	if unsupported.saveCalls != 0 {
		t.Fatal("unsupported store received an unfenced start write")
	}

	unsafeTestRunner := NewTestRunner(1, NewHub(), nil, withUnfencedDeploymentLeaseStartForFaultInjection())
	spy := &metricsStoreSpy{}
	unsafeTestRunner.testStore = spy
	if err := unsafeTestRunner.saveManagedTestStartRecord(&ManagedTest{ID: "fault-injection", Status: "INITIALIZING", ProjectID: storage.DefaultProjectID}); err != nil {
		t.Fatalf("test-only fault-injection seam returned error: %v", err)
	}
	if len(spy.savedTests) != 1 {
		t.Fatalf("test-only fault-injection saves = %d, want 1", len(spy.savedTests))
	}
}

func TestDeploymentLeaseCISmokeAuthorizationRequiresTokenDigest(t *testing.T) {
	source := ciTestRunSource("2")
	owner := "github:" + source.Repository + ":" + source.RunID + ":" + source.RunAttempt
	rawToken := strings.Repeat("a", 64)
	for _, test := range []struct {
		name     string
		digest   string
		wantAuth bool
	}{
		{name: "missing token"},
		{name: "valid token", digest: deploymentLeaseTokenHash(rawToken), wantAuth: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := ciRunMetadata{SchemaVersion: ciRunSchemaVersion, Source: source, DeploymentLeaseTokenSHA: test.digest}
			encoded, err := encodeCIRunMetadata(metadata)
			if err != nil {
				t.Fatal(err)
			}
			testRun := &ManagedTest{MaterializationKey: encoded}
			gotOwner, gotDigest := deploymentLeaseAuthorizationForCITest(testRun)
			if test.wantAuth {
				if gotOwner != owner || gotDigest != test.digest {
					t.Fatalf("authorization = (%q, %q)", gotOwner, gotDigest)
				}
			} else if gotOwner != "" || gotDigest != "" {
				t.Fatalf("unauthorized metadata produced (%q, %q)", gotOwner, gotDigest)
			}
			if strings.Contains(encoded, rawToken) {
				t.Fatal("raw deployment lease token was persisted in CI metadata")
			}
		})
	}
}

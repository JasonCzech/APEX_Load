package api

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// A CI run's metadata names the Actions policy revision that accepted it. A
// queued run whose metadata no longer decodes cannot be checked against the
// current policy, so it must not start as if it were not a CI run.
func TestCIRunPolicyRefusesUnreadableCIRunMetadata(t *testing.T) {
	for name, key := range map[string]string{
		"invalid base64":     ciRunMetadataPrefix + "%%%",
		"invalid JSON":       ciRunMetadataPrefix + base64.RawURLEncoding.EncodeToString([]byte("{")),
		"unsupported schema": ciRunMetadataPrefix + base64.RawURLEncoding.EncodeToString([]byte(`{"schema_version":999}`)),
	} {
		t.Run(name, func(t *testing.T) {
			runner := NewTestRunner(2, NewHub(), nil)
			runner.tests["ci-unreadable"] = &ManagedTest{
				ID: "ci-unreadable", Status: "QUEUED", ProjectID: storage.DefaultProjectID, MaterializationKey: key,
			}
			err := runner.validateCIRunPolicyForStart("ci-unreadable")
			if err == nil || !strings.Contains(err.Error(), "CI run metadata is unreadable") {
				t.Fatalf("validateCIRunPolicyForStart error = %v, want unreadable CI run metadata", err)
			}
		})
	}
}

func TestCIRunPolicyIgnoresTestsThatAreNotCIRuns(t *testing.T) {
	// No project store: reaching the policy lookup would report it unavailable.
	runner := NewTestRunner(2, NewHub(), nil)
	runner.tests["scenario-run"] = &ManagedTest{
		ID: "scenario-run", Status: "QUEUED", ProjectID: storage.DefaultProjectID, MaterializationKey: "scenario:schedule-1",
	}
	if err := runner.validateCIRunPolicyForStart("scenario-run"); err != nil {
		t.Fatalf("validateCIRunPolicyForStart error = %v, want nil for a test that is not a CI run", err)
	}
}

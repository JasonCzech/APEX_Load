package duckdb

import (
	"errors"

	"github.com/shirou/gopsutil/v3/process"
	"os"
	"testing"
)

func TestWorkerMissingChildObservationIsExactAndNeverGenericProof(t *testing.T) {
	child := &os.Process{Pid: 1234}
	missing := workerChildObservationError(child, process.ErrorProcessNotRunning)
	if !workerObservationMissingChild(missing, child) || workerObservationMissingChild(missing, &os.Process{Pid: 1235}) || workerObservationMissingChild(errWorkerResourceLimit, child) || workerObservationMissingChild(missing, nil) {
		t.Fatal("missing inspection escaped exact identity")
	}
	for _, err := range []error{os.ErrPermission, errors.New("unclassified inspection failure"), nil} {
		if got := workerChildObservationError(child, err); got != errWorkerResourceLimit {
			t.Fatal("generic failure became absence", got)
		}
	}
	if got := workerChildObservationError(nil, process.ErrorProcessNotRunning); got != errWorkerResourceLimit {
		t.Fatal("missing process identity authorized observation", got)
	}
}

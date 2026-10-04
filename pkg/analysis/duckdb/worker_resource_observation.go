package duckdb

import (
	"errors"

	"github.com/shirou/gopsutil/v3/process"
	"os"
)

// Missing child inspection is not evidence of physical exit or closure. Only
// the exact supervisor may reconcile it with actual Wait and a valid terminal
// result; all other observation failures remain fail-closed resource denials.
type workerMissingProcessObservation struct{ pid int }

func (*workerMissingProcessObservation) Error() string {
	return "native child resource observation absent"
}
func workerChildObservationError(child *os.Process, err error) error {
	if child != nil && child.Pid > 0 && (errors.Is(err, process.ErrorProcessNotRunning) || workerPlatformProcessAbsent(err)) {
		return &workerMissingProcessObservation{pid: child.Pid}
	}
	return errWorkerResourceLimit
}
func workerObservationMissingChild(err error, child *os.Process) bool {
	observation, ok := err.(*workerMissingProcessObservation)
	return ok && observation != nil && child != nil && child.Pid > 0 && observation.pid == child.Pid
}

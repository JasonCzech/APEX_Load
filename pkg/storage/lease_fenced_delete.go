package storage

import (
	"fmt"
	"strings"
)

// LeaseFencedDeleteResult distinguishes a completed destructive mutation from
// the benign races callers must handle explicitly. In particular, Fenced means
// the supplied execution epoch no longer owns the durable lease; callers must
// not update replica-local indexes after receiving it.
type LeaseFencedDeleteResult string

const (
	LeaseFencedDeleteDeleted LeaseFencedDeleteResult = "deleted"
	LeaseFencedDeleteMissing LeaseFencedDeleteResult = "missing"
	LeaseFencedDeleteActive  LeaseFencedDeleteResult = "active"
	LeaseFencedDeleteFenced  LeaseFencedDeleteResult = "fenced"
)

func validateLeaseFencedDelete(leaseName, leaseOwnerID string) (string, string, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if leaseName == "" || leaseOwnerID == "" {
		return "", "", fmt.Errorf("lease name and owner token are required")
	}
	return leaseName, leaseOwnerID, nil
}

func activeTestStatusForDeletion(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "INITIALIZING", "STARTING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
		return true
	default:
		return false
	}
}

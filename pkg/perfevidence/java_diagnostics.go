package perfevidence

import (
	"errors"
	"math"
	"regexp"

	"github.com/jasonczech/apexload/pkg/engine"
)

var diagnosticIdentity = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validateJavaDiagnostics(hosts []engine.JavaHostEvidence, users int) error {
	if len(hosts) > 256 {
		return errors.New("too many Java diagnostic hosts")
	}
	seenHosts, seenSlots := map[string]bool{}, map[string]bool{}
	for _, h := range hosts {
		if !diagnosticIdentity.MatchString(h.HostID) || seenHosts[h.HostID] || len(h.SlotIDs) == 0 || len(h.SlotIDs) > 256 || h.Observations < 0 || h.ReadFailures < 0 || len(h.GCTail) > 128 || h.GCPauseCount > math.MaxInt64 || h.GCMissing > math.MaxInt64-h.GCPauseCount {
			return errors.New("invalid Java host diagnostics")
		}
		seenHosts[h.HostID] = true
		for _, id := range h.SlotIDs {
			if !diagnosticIdentity.MatchString(id) || seenSlots[id] {
				return errors.New("invalid Java diagnostic slot")
			}
			seenSlots[id] = true
		}
		switch h.GCStatus {
		case "unobserved", "unsupported", "available", "failed":
		default:
			return errors.New("invalid Java GC capture status")
		}
		for i, n := range h.OutputBytes {
			limit := 256 << 10
			if i == 2 {
				limit = 32768
			}
			if n < 0 || n > limit || h.OutputDropped[i] > 0 && !h.OutputTruncated {
				return errors.New("invalid Java output counts")
			}
		}
		for i, event := range h.GCTail {
			if event.Sequence == 0 || event.Sequence > math.MaxInt64 || event.StartUnixNano <= 0 || event.DurationNano < 0 || event.DurationNano > math.MaxInt64-event.StartUnixNano || i > 0 && event.Sequence != h.GCTail[i-1].Sequence+1 {
				return errors.New("invalid Java GC tail")
			}
		}
		if len(h.GCTail) > 0 && h.GCTail[len(h.GCTail)-1].Sequence != h.GCPauseCount+h.GCMissing {
			return errors.New("inconsistent Java GC event counts")
		}
	}
	if len(seenSlots) > users {
		return errors.New("diagnostic population exceeds admitted users")
	}
	return nil
}
func completeJavaDiagnostics(hosts []engine.JavaHostEvidence, users int) bool {
	if validateJavaDiagnostics(hosts, users) != nil {
		return false
	}
	slots := 0
	for _, h := range hosts {
		if h.Observations == 0 || h.ReadFailures != 0 || h.OutputTruncated || h.GCMissing != 0 || h.GCStatus != "available" {
			return false
		}
		slots += len(h.SlotIDs)
	}
	return slots == users
}

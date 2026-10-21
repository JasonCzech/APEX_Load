package protocol

import "fmt"

// Capture failures precede artifact transfer. They retain no target text or
// paths; archives carry these counts through the existing omission map.
const (
	BrowserHAROmittedCaptureFailed      = "native_capture_failed"
	BrowserHAROmittedFinalizationBudget = "native_finalization_budget"
	BrowserHAROmittedOwnerUnretired     = "native_owner_unretired"
	BrowserHAROmittedPublicationLimit   = "native_publication_limit"
	BrowserHAROmittedCaptureStatus      = "native_status_unavailable"
	BrowserHAROmittedCountLimit         = "native_omission_count_limit"
	MaxBrowserHAROmissionCount          = int64(MaxBrowserHARArtifactsPerRun * 1024)
)

// BrowserHARCaptureOmissionProvider complements BrowserHARArtifactProvider.
// A take hands off each loss once. A later successful recovery cannot erase a
// loss already reported by an earlier finalization boundary.
type BrowserHARCaptureOmissionProvider interface {
	TakeBrowserHARCaptureOmissions() map[string]int64
}

// MergeBrowserHARCaptureOmissions validates before making a copy. Saturation
// is explicit and matches the remote manifest reader's count bound. Existing
// transfer/export reasons are preserved; added capture reasons are closed.
func MergeBrowserHARCaptureOmissions(current, added map[string]int64) (map[string]int64, error) {
	if len(added) > 6 {
		return nil, fmt.Errorf("invalid native HAR omission population")
	}
	for reason, count := range added {
		switch reason {
		case BrowserHAROmittedCaptureFailed, BrowserHAROmittedFinalizationBudget, BrowserHAROmittedOwnerUnretired, BrowserHAROmittedPublicationLimit, BrowserHAROmittedCaptureStatus:
		case BrowserHAROmittedCountLimit:
			if count != 1 {
				return nil, fmt.Errorf("invalid native HAR omission limit marker")
			}
		default:
			return nil, fmt.Errorf("invalid native HAR omission reason")
		}
		if count <= 0 || count > MaxBrowserHAROmissionCount {
			return nil, fmt.Errorf("invalid native HAR omission count")
		}
	}
	if len(added) == 0 {
		return current, nil
	}
	out := make(map[string]int64, len(current)+len(added))
	for reason, count := range current {
		out[reason] = count
	}
	for reason, count := range added {
		if reason == BrowserHAROmittedCountLimit {
			out[reason] = 1
			continue
		}
		previous := out[reason]
		if previous < 0 || previous > MaxBrowserHAROmissionCount-count {
			out[reason] = MaxBrowserHAROmissionCount
			out[BrowserHAROmittedCountLimit] = 1
		} else {
			out[reason] = previous + count
		}
	}
	return out, nil
}

package browser

import (
	"errors"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func (b *Browser) noteHARCaptureOmission(owner *harCaptureOwner, reason string) {
	if owner == nil {
		return
	}
	owner.omissionOnce.Do(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.harCaptureOmissions, _ = protocol.MergeBrowserHARCaptureOmissions(b.harCaptureOmissions, map[string]int64{reason: 1})
	})
}

func (b *Browser) reportHARFinalizationFailure(owner *harCaptureOwner, err error) {
	reason := protocol.BrowserHAROmittedCaptureFailed
	if errors.Is(err, errHARFinalizationBudget) {
		reason = protocol.BrowserHAROmittedFinalizationBudget
	} else if !owner.retiredSuccessfully() {
		reason = protocol.BrowserHAROmittedOwnerUnretired
	}
	b.noteHARCaptureOmission(owner, reason)
}

// TakeBrowserHARCaptureOmissions transfers bounded counts without changing
// writer ownership, leases, cleanup eligibility or the target run outcome.
func (b *Browser) TakeBrowserHARCaptureOmissions() map[string]int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.harCaptureOmissions
	b.harCaptureOmissions = nil
	return out
}

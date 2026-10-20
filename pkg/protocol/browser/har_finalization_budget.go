package browser

import (
	"errors"
	"time"
)

const runtimeHARFinalizationBudget = 30 * time.Second

var errHARFinalizationBudget = errors.New("browser HAR finalization budget exhausted")

// A caller's short wait and physical ownership are independent of the capture
// deadline. Expiration refuses publication; it never asserts that a writer has
// stopped. Joins continue to own their leases and admission charges.
func (o *harCaptureOwner) startFinalizationBudget() time.Time {
	o.finalizationOnce.Do(func() {
		o.mu.Lock()
		if o.finalizationDeadline.IsZero() {
			o.finalizationDeadline = time.Now().Add(runtimeHARFinalizationBudget)
		}
		o.finalizationTimer = time.AfterFunc(time.Until(o.finalizationDeadline), func() { _ = o.finalizationBudgetError() })
		o.mu.Unlock()
	})
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.finalizationDeadline
}

func (o *harCaptureOwner) finalizationBudgetError() error {
	o.mu.Lock()
	expired := o.finalizationReported || !o.finalizationComplete && !o.finalizationDeadline.IsZero() && !time.Now().Before(o.finalizationDeadline)
	report := expired && !o.finalizationReported && !o.finalizationComplete
	if report {
		o.finalizationReported = true
	}
	o.mu.Unlock()
	if report {
		browserEvidenceBudget.counters.harFinalizationExhausted.Add(1)
		logEvidenceEvent("browser HAR finalization budget exhausted", "reason", "har_finalization_budget_exhausted", "budget_seconds", 30)
	}
	if expired {
		return errHARFinalizationBudget
	}
	return nil
}

func (o *harCaptureOwner) finishFinalizationBudget() {
	o.mu.Lock()
	o.finalizationComplete = true
	if o.finalizationTimer != nil {
		o.finalizationTimer.Stop()
	}
	o.mu.Unlock()
}

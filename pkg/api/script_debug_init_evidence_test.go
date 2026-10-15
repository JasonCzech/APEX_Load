package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// A recorded sign-in the conversion moved to init has no observation in the
// recording's baseline, so its replay events are skipped: they must not
// discard the evidence the iteration's steps give.
func TestDebugCorrelationEvidenceSkipsInitSteps(t *testing.T) {
	session := &ScriptDebugSession{ID: "init-evidence", Status: ScriptDebugRunning}
	evidence := func(target string) *engine.ReplayActionEvent {
		return &engine.ReplayActionEvent{Success: true, CorrelationEvidence: &protocol.CorrelationEvidence{
			RequestURL: "https://example.test" + target, AuthoredRequestURL: "https://example.test" + target,
		}}
	}
	signIn := evidence("/services/oauth2/token")
	signIn.Phase, signIn.ActionIndex = "init", 0
	session.appendCorrelationObservationLocked(engine.ReplayDebugAction{Phase: "init", ActionIndex: 0}, signIn, time.Now())
	// A login page too large to keep is no reason to drop the iteration's.
	loginPage := &engine.ReplayActionEvent{Phase: "init", ActionIndex: 1, CorrelationEvidence: protocol.UnavailableCorrelationEvidence(protocol.CorrelationEvidenceTooLargeReason)}
	session.appendCorrelationObservationLocked(engine.ReplayDebugAction{Phase: "init", ActionIndex: 1}, loginPage, time.Now())
	step := evidence("/services/data/v66.0/query")
	step.Phase, step.ActionIndex = "action", 0
	session.appendCorrelationObservationLocked(engine.ReplayDebugAction{Phase: "action", ActionIndex: 0}, step, time.Now())
	if session.correlationEvidenceErr != "" {
		t.Fatalf("evidence rejected: %s", session.correlationEvidenceErr)
	}
	if len(session.correlationObservations) != 1 || session.correlationObservations[0].ID != 0 || session.correlationObservations[0].RequestURL != "https://example.test/services/data/v66.0/query" {
		t.Fatalf("observations = %+v; want only the iteration's step", session.correlationObservations)
	}
}

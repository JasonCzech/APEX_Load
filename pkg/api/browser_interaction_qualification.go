package api

import (
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func interactionQualificationReason(i protocol.BrowserInteraction) string {
	if i.Version != protocol.BrowserInteractionVersion2 {
		return "legacy_identity_unavailable"
	}
	if !i.AuthoredStep.Valid() {
		return "authored_step_unavailable"
	}
	if !i.Execution.Valid() {
		return "execution_unavailable"
	}
	if i.Dispatch == nil || !i.Dispatch.Valid(i.SourceGeneration) {
		return "dispatch_unavailable"
	}
	return ""
}
func interactionClock(i protocol.BrowserInteraction) time.Time {
	if i.Version == protocol.BrowserInteractionVersion2 && i.Dispatch != nil {
		return i.Dispatch.At
	}
	return i.CapturedAt
}
func sameInvocation(a, b protocol.BrowserInteraction) bool {
	return a.Version == protocol.BrowserInteractionVersion2 && b.Version == a.Version && a.SourceGeneration == b.SourceGeneration && sameInteractionStep(a, b) && a.Execution.Valid() && b.Execution.Valid() && a.Execution.Invocation == b.Execution.Invocation
}

func interactionInvocationKey(i protocol.BrowserInteraction) string {
	if !i.AuthoredStep.Valid() || !i.Execution.Valid() {
		return ""
	}
	return i.SourceGeneration + "\x00" + i.AuthoredStep.StepID + "\x00" + strconv.FormatUint(i.Execution.Invocation, 10)
}

func (a *interactionAnalyzer) patternQualificationReason(i protocol.BrowserInteraction) string {
	if reason := interactionQualificationReason(i); reason != "" {
		return reason
	}
	cohort, ok := a.cohorts[i.SourceGeneration]
	if !ok || cohort.engine == "" || cohort.engine == "unknown" || cohort.profile == "unknown" {
		return "engine_profile_unknown"
	}
	if !a.documentJoins[interactionDocumentJoin(i.SourceGeneration, i.Dispatch.DocumentID, i.Dispatch.LifecycleID)] {
		return "document_lifecycle_unavailable"
	}
	return ""
}

// Bound joins independently of the interaction sample and context attribution.
// Revisions/metrics for the same physical lifecycle consume one entry.
const maxInteractionDocumentJoins = 100000

func interactionDocumentJoin(generation, document, lifecycle string) string {
	return generation + "\x00" + document + "\x00" + lifecycle
}
func (c *browserGenerationCohorts) addDocumentJoin(o protocol.BrowserDocumentObservation) {
	if o.SourceGeneration == "" || o.DocumentID == "" || o.LifecycleID == "" {
		return
	}
	if c.joins == nil {
		c.joins = map[string]bool{}
	}
	key := interactionDocumentJoin(o.SourceGeneration, o.DocumentID, o.LifecycleID)
	if c.joins[key] {
		return
	}
	bound := c.joinBound
	if bound <= 0 {
		bound = maxInteractionDocumentJoins
	}
	if len(c.joins) >= bound {
		c.joinsTruncated = true
		return
	}
	c.joins[key] = true
}

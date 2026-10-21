package protocol

import (
	"math"
	"time"
)

// BrowserInteractionVersion2 adds authored identity and independently observed
// dispatch clocks. Its producer must be negotiated with the receiving peer.
// Version 1 remains readable and never acquires inferred dispatch data.
const BrowserInteractionVersion2 = 2
const (
	InteractionDispatchClockUnavailable    = "clock_unavailable"
	InteractionDispatchDocumentUnavailable = "document_identity_unavailable"
	InteractionDispatchActionUnavailable   = "action_identity_unavailable"
)

// BrowserInteractionDispatch identifies the first relevant trusted native input
// event. TimeOriginMS and OffsetMS are the document's performance.timeOrigin and
// the event's timeStamp; At is their UTC projection, not the controller clock.
// Geometry on the containing interaction describes this same map anchor.
// Missing dispatch means the gesture cannot qualify a dispatch-time window.
type BrowserInteractionDispatch struct {
	At           time.Time `json:"at"`
	TimeOriginMS float64   `json:"time_origin_ms"`
	OffsetMS     float64   `json:"offset_ms"`
	DocumentID   string    `json:"document_id"`
	LifecycleID  string    `json:"lifecycle_id"`
}

func (d *BrowserInteractionDispatch) Valid(generation string) bool {
	if d == nil || !BrowserEvidenceGeneration(generation) ||
		!browserEvidenceID(d.DocumentID, generation, false) ||
		!browserEvidenceID(d.LifecycleID, generation, false) ||
		!browserInteractionFinite(d.TimeOriginMS, 253402300799999) || d.TimeOriginMS <= 0 ||
		!browserInteractionFinite(d.OffsetMS, 86400000) || d.OffsetMS < 0 ||
		d.At.IsZero() || d.At.Location() != time.UTC {
		return false
	}
	ms := d.TimeOriginMS + d.OffsetMS
	if ms > 253402300799999 {
		return false
	}
	want := time.Unix(int64(ms/1000), int64(math.Mod(ms, 1000)*1e6)).UTC()
	return d.At.Equal(want)
}

func browserInteractionV2FieldsValid(i BrowserInteraction) bool {
	if i.Version == BrowserInteractionVersion {
		return i.Execution == nil && i.AuthoredStep == nil && i.Dispatch == nil && i.DispatchUnavailableReason == ""
	}
	if i.Execution != nil && (!i.Execution.Valid() || i.AuthoredStep == nil) {
		return false
	}
	if i.Version != BrowserInteractionVersion2 || i.AuthoredStep != nil && !i.AuthoredStep.Valid() {
		return false
	}
	if i.Dispatch != nil {
		return i.DispatchUnavailableReason == "" && i.FrameScope == "main_frame" &&
			i.Dispatch.Valid(i.SourceGeneration) && i.DocumentID == i.Dispatch.DocumentID
	}
	switch i.DispatchUnavailableReason {
	case "not_observed", InteractionDispatchClockUnavailable, InteractionDispatchDocumentUnavailable, InteractionDispatchActionUnavailable, InteractionUnavailablePageState, InteractionUnavailableChildFrame,
		InteractionUnavailableSyntheticDispatch, InteractionUnavailableDocumentChanged:
		return true
	}
	return false
}

// BrowserInteractionEvidenceV2Provider guarantees that the runtime can emit
// either v1 or explicitly selected v2. It is capability, not capture enablement.
type BrowserInteractionEvidenceV2Provider interface {
	SupportsBrowserInteractionEvidenceV2()
}

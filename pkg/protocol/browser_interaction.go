package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"
)

// Browser interaction evidence (E3). One record per scripted gesture: what
// was dispatched (the CSS points the page actually received from trusted
// input events, or for keyboard input the focused target's role and hashed
// sanitized label, never a value), the viewport, scroll and device scale,
// a structural layout revision, the sanitized route, the declared outcome
// and the observed result, and the gesture's provenance. Capture reads the
// page after the action's own wait. V2 also captures a native-event clock
// and geometry, independently of that later read. Neither adds target traffic.
const (
	BrowserInteractionVersion             = 1
	MaxBrowserInteractionsPerDocument     = 1024
	MaxBrowserInteractionBytesPerDocument = 64 << 10
	MaxBrowserInteractionEvidenceBytes    = 512 << 10
	MaxBrowserInteractionsPerEvidence     = 4096
	maxBrowserInteractionPoints           = 8

	InteractionProvenanceScripted = "scripted"
	InteractionProvenanceRecorded = "recorded"
	InteractionProvenanceUnknown  = "unknown"

	InteractionOutcomeNotDeclared = "not_declared"
	InteractionOutcomeMet         = "met"
	InteractionOutcomeMissed      = "missed"
	// InteractionOutcomeNotReached: the gesture failed before its declared
	// outcome was waited for, or v2 could not evaluate it (cancellation, page
	// loss, invalid selector or unsupported wait).
	InteractionOutcomeNotReached  = "not_reached"
	InteractionOutcomeUnsupported = "unsupported"

	InteractionOutcomeKindSync       = "sync"
	InteractionOutcomeKindDialog     = "dialog"
	InteractionOutcomeKindAssertion  = "assertion"
	InteractionOutcomeKindDownload   = "download"
	InteractionOutcomeKindNavigation = "navigation"

	MaxBrowserInteractionOutcomes = 16
)

// Reasons an interaction's points or target were not observed.
const (
	// InteractionUnavailablePageState: the page could not be read after the
	// gesture.
	InteractionUnavailablePageState = "page_state_unavailable"
	// InteractionUnavailableChildFrame: the gesture acted inside a child
	// frame, which the main document's observer does not see.
	InteractionUnavailableChildFrame = "child_frame_unobserved"
	// InteractionUnavailableSyntheticDispatch: the runtime dispatched the
	// gesture as synthetic events (touch emulation), which a page cannot tell
	// from script and the observer ignores.
	InteractionUnavailableSyntheticDispatch = "synthetic_dispatch"
	// InteractionUnavailableDocumentChanged: the gesture navigated, so its
	// document's events went with it.
	InteractionUnavailableDocumentChanged = "document_changed"
	// InteractionUnavailableTarget: the element a keyboard gesture acted on
	// could not be resolved after it.
	InteractionUnavailableTarget = "target_unresolved"
	// InteractionUnavailableCrossOriginAncestry: a child frame's ancestor chain
	// crosses a cross-origin or opaque-sandboxed boundary.
	InteractionUnavailableCrossOriginAncestry = "cross_origin_ancestry"
	// InteractionUnavailableNonAxisAlignedOr3DTransform: a child frame's
	// ancestor chain uses a 3D, perspective, or non-invertible transform.
	InteractionUnavailableNonAxisAlignedOr3DTransform = "non_axis_aligned_or_3d_transform"
	// InteractionUnavailableFrameDepthExceeded: a child frame's nesting depth
	// exceeds MaxBrowserInteractionFrameDepth.
	InteractionUnavailableFrameDepthExceeded = "frame_depth_exceeded"
)

var browserInteractionKinds = map[string]bool{
	"click": true, "dblclick": true, "tap": true, "long_press": true, "swipe": true, "hover": true, "drag": true, "scroll": true,
	"check": true, "key": true, "fill": true, "select": true, "set_files": true, "download": true,
}

// BrowserInteractionPointerKinds dispatch pointer input; the others are
// keyboard or form input recorded by target, without coordinates.
func BrowserInteractionPointerKind(kind string) bool {
	switch kind {
	case "click", "dblclick", "tap", "long_press", "swipe", "hover", "drag", "check", "download":
		return true
	}
	return false
}

// BrowserInteractionPoint is a CSS-pixel point in the frame's viewport.
type BrowserInteractionPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// BrowserInteractionTarget identifies a keyboard target without its text.
type BrowserInteractionTarget struct {
	Role        string `json:"role"`
	LabelSHA256 string `json:"label_sha256,omitempty"`
}

// BrowserInteractionOutcome is a gesture's declared outcome (wait condition,
// dialog guard, inline assertion, download, or navigation commit) and what was
// observed.
type BrowserInteractionOutcome struct {
	Kind               string               `json:"kind,omitempty"`
	Clock              *BrowserOutcomeClock `json:"clock,omitempty"`
	ObservedAt         *time.Time           `json:"observed_at,omitempty"`
	Declared           string               `json:"declared,omitempty"`
	DeadlineMS         int                  `json:"deadline_ms,omitempty"`
	Observed           string               `json:"observed"`
	Reason             string               `json:"reason,omitempty"`
	NavigationSequence uint64               `json:"navigation_sequence,omitempty"`
	AuthoredPageID     string               `json:"authored_page_id,omitempty"`
	RouteSeq           uint64               `json:"route_seq,omitempty"`
}

type BrowserInteraction struct {
	Execution                 *BrowserActionExecution      `json:"execution,omitempty"`
	AuthoredStep              *BrowserAuthoredStep         `json:"authored_step,omitempty"`
	Dispatch                  *BrowserInteractionDispatch  `json:"dispatch,omitempty"`
	DispatchUnavailableReason string                       `json:"dispatch_unavailable_reason,omitempty"`
	GeometryProvenance        string                       `json:"geometry_provenance,omitempty"`
	AuthoredPageID            string                       `json:"authored_page_id,omitempty"`
	RouteSeq                  uint64                       `json:"route_seq,omitempty"`
	FrameChain                []BrowserInteractionFrameHop `json:"frame_chain,omitempty"`

	Version           int                         `json:"version"`
	SourceGeneration  string                      `json:"source_generation"`
	InteractionID     string                      `json:"interaction_id"`
	ActionID          string                      `json:"action_id"`
	ActionName        string                      `json:"action_name,omitempty"`
	DocumentID        string                      `json:"document_id,omitempty"`
	FrameScope        string                      `json:"frame_scope"`
	Kind              string                      `json:"kind"`
	PointerType       string                      `json:"pointer_type,omitempty"`
	Points            []BrowserInteractionPoint   `json:"points,omitempty"`
	Target            *BrowserInteractionTarget   `json:"target,omitempty"`
	ViewportWidth     int                         `json:"viewport_width,omitempty"`
	ViewportHeight    int                         `json:"viewport_height,omitempty"`
	ScrollX           float64                     `json:"scroll_x"`
	ScrollY           float64                     `json:"scroll_y"`
	DeviceScaleFactor float64                     `json:"device_scale_factor,omitempty"`
	LayoutRevision    string                      `json:"layout_revision,omitempty"`
	Route             string                      `json:"route,omitempty"`
	Outcome           BrowserInteractionOutcome   `json:"outcome,omitzero"`
	Outcomes          []BrowserInteractionOutcome `json:"outcomes,omitempty"`
	Provenance        string                      `json:"provenance"`
	Success           bool                        `json:"success"`
	CapturedAt        time.Time                   `json:"captured_at"`
	UnavailableReason string                      `json:"unavailable_reason,omitempty"`
}

// PrimaryOutcome returns the single v1-v3 outcome or the first v4 outcome.
func (i BrowserInteraction) PrimaryOutcome() BrowserInteractionOutcome {
	if i.Version >= BrowserInteractionVersion4 && len(i.Outcomes) > 0 {
		return i.Outcomes[0]
	}
	return i.Outcome
}

// DeclaredOutcomes returns the interaction's outcomes across all wire versions.
func (i BrowserInteraction) DeclaredOutcomes() []BrowserInteractionOutcome {
	if i.Version >= BrowserInteractionVersion4 {
		return i.Outcomes
	}
	return []BrowserInteractionOutcome{i.Outcome}
}

// BrowserInteractionEvidence is a bounded batch of one source generation's
// interactions; Dropped counts interactions its bounds left out.
type BrowserInteractionEvidence struct {
	Version          int                  `json:"version"`
	SourceGeneration string               `json:"source_generation"`
	Interactions     []BrowserInteraction `json:"interactions"`
	Dropped          int64                `json:"dropped"`
	Incomplete       bool                 `json:"incomplete"`
}

func browserInteractionFinite(v, limit float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) <= limit
}

// Valid checks one interaction record.
func (i BrowserInteraction) Valid() bool {
	if !browserInteractionOutcomesValid(i) {
		return false
	}
	if !browserInteractionV2FieldsValid(i) || !BrowserEvidenceGeneration(i.SourceGeneration) || !browserEvidenceID(i.InteractionID, i.SourceGeneration, false) || !browserEvidenceID(i.ActionID, i.SourceGeneration, true) || !browserEvidenceID(i.DocumentID, i.SourceGeneration, true) || !browserEvidenceText(i.ActionName, 256) || !browserInteractionKinds[i.Kind] {
		return false
	}
	if i.FrameScope != "main_frame" && i.FrameScope != "child_frame" {
		return false
	}
	switch i.Provenance {
	case InteractionProvenanceScripted, InteractionProvenanceRecorded, InteractionProvenanceUnknown:
	default:
		return false
	}
	if len(i.Points) > maxBrowserInteractionPoints || len(i.Points) > 0 && !BrowserInteractionPointerKind(i.Kind) {
		return false
	}
	for _, p := range i.Points {
		if !browserInteractionFinite(p.X, 1e6) || !browserInteractionFinite(p.Y, 1e6) {
			return false
		}
	}
	switch i.PointerType {
	case "", "mouse", "touch", "pen":
	default:
		return false
	}
	if i.Target != nil {
		if BrowserInteractionPointerKind(i.Kind) && i.Kind != "check" || !browserInteractionRole(i.Target.Role) {
			return false
		}
		if i.Target.LabelSHA256 != "" {
			if sum, err := hex.DecodeString(i.Target.LabelSHA256); err != nil || len(sum) != 32 || strings.ToLower(i.Target.LabelSHA256) != i.Target.LabelSHA256 {
				return false
			}
		}
	}
	if i.ViewportWidth < 0 || i.ViewportWidth > 100000 || i.ViewportHeight < 0 || i.ViewportHeight > 100000 || !browserInteractionFinite(i.ScrollX, 1e9) || !browserInteractionFinite(i.ScrollY, 1e9) || !browserInteractionFinite(i.DeviceScaleFactor, 100) || i.DeviceScaleFactor < 0 {
		return false
	}
	if len(i.LayoutRevision) > 64 || !browserInteractionToken(i.LayoutRevision) || !browserEvidenceText(i.Route, 512) || strings.ContainsAny(i.Route, "?#") || i.Route != "" && !strings.HasPrefix(i.Route, "/") {
		return false
	}
	return !i.CapturedAt.IsZero() && i.CapturedAt.Location() == time.UTC && browserInteractionReason(i.UnavailableReason)
}

func browserInteractionOutcomesValid(i BrowserInteraction) bool {
	if i.Version == BrowserInteractionVersion4 {
		if i.Outcome != (BrowserInteractionOutcome{}) || len(i.Outcomes) == 0 || len(i.Outcomes) > MaxBrowserInteractionOutcomes {
			return false
		}
		for _, o := range i.Outcomes {
			if !o.validV4(i.CapturedAt, len(i.Outcomes)) {
				return false
			}
		}
		return true
	}
	if len(i.Outcomes) != 0 || i.Outcome.Kind != "" || i.Outcome.Reason != "" || i.Outcome.ObservedAt != nil || i.Outcome.NavigationSequence != 0 || i.Outcome.AuthoredPageID != "" || i.Outcome.RouteSeq != 0 {
		return false
	}
	if i.Version == BrowserInteractionVersion3 && (i.Outcome.Observed == InteractionOutcomeMet || i.Outcome.Observed == InteractionOutcomeMissed) {
		if !i.Outcome.Clock.Valid() || i.Outcome.Observed == InteractionOutcomeMet && i.Outcome.Clock.ObservedAt.After(i.Outcome.Clock.DeadlineAt) || i.Outcome.Observed == InteractionOutcomeMissed && i.Outcome.Clock.ObservedAt.Before(i.Outcome.Clock.DeadlineAt) {
			return false
		}
	}
	if i.Outcome.Clock != nil && (i.Version != BrowserInteractionVersion3 || !i.Outcome.Clock.Valid() || i.Outcome.Clock.BudgetMS != i.Outcome.DeadlineMS || i.Outcome.Declared == "" || i.Outcome.Clock.ObservedAt.After(i.CapturedAt)) {
		return false
	}
	switch i.Outcome.Observed {
	case InteractionOutcomeNotDeclared:
		return i.Outcome.Declared == "" && i.Outcome.DeadlineMS == 0
	case InteractionOutcomeMet, InteractionOutcomeMissed, InteractionOutcomeNotReached:
		return browserEvidenceText(i.Outcome.Declared, 64) && i.Outcome.Declared != "" && i.Outcome.DeadlineMS >= 0 && i.Outcome.DeadlineMS <= 86400000
	default:
		return false
	}
}

func (o BrowserInteractionOutcome) validV4(capturedAt time.Time, totalOutcomes int) bool {
	if !browserEvidenceText(o.Reason, 64) {
		return false
	}
	if o.Observed == InteractionOutcomeNotDeclared {
		return totalOutcomes == 1 && o.Kind == InteractionOutcomeKindSync && o.Declared == "" && o.DeadlineMS == 0 && o.Reason == "" && o.ObservedAt == nil && o.NavigationSequence == 0 && o.AuthoredPageID == "" && o.RouteSeq == 0 && o.Clock == nil
	}
	switch o.Observed {
	case InteractionOutcomeMet, InteractionOutcomeMissed, InteractionOutcomeNotReached, InteractionOutcomeUnsupported:
	default:
		return false
	}
	if o.Declared == "" || !browserEvidenceText(o.Declared, 64) {
		return false
	}
	switch o.Kind {
	case InteractionOutcomeKindSync, InteractionOutcomeKindDialog, InteractionOutcomeKindDownload:
		if o.DeadlineMS <= 0 || o.DeadlineMS > 86400000 || o.ObservedAt != nil || o.NavigationSequence != 0 || o.AuthoredPageID != "" || o.RouteSeq != 0 {
			return false
		}
		if o.Observed == InteractionOutcomeMet || o.Observed == InteractionOutcomeMissed {
			if !o.Clock.Valid() || o.Clock.BudgetMS != o.DeadlineMS || o.Clock.ObservedAt.After(capturedAt) {
				return false
			}
			if o.Observed == InteractionOutcomeMet && o.Clock.ObservedAt.After(o.Clock.DeadlineAt) {
				return false
			}
			if o.Observed == InteractionOutcomeMissed && (o.Reason == "" || o.Reason == "deadline_missed") && o.Clock.ObservedAt.Before(o.Clock.DeadlineAt) {
				return false
			}
			return true
		}
		return o.Clock == nil
	case InteractionOutcomeKindAssertion:
		if o.DeadlineMS != 0 || o.Clock != nil || o.NavigationSequence != 0 || o.AuthoredPageID != "" || o.RouteSeq != 0 {
			return false
		}
		if o.Observed == InteractionOutcomeMet || o.Observed == InteractionOutcomeMissed {
			if o.ObservedAt == nil || !browserOutcomeTime(*o.ObservedAt) || o.ObservedAt.After(capturedAt) {
				return false
			}
			return o.Observed == InteractionOutcomeMet || o.Reason != ""
		}
		return o.ObservedAt == nil && o.Reason != ""
	case InteractionOutcomeKindNavigation:
		if o.DeadlineMS != 0 || o.Clock != nil || o.NavigationSequence > 9007199254740991 || o.RouteSeq > 9007199254740991 {
			return false
		}
		if o.AuthoredPageID != "" && (len(o.AuthoredPageID) != 64 || !browserInteractionToken(o.AuthoredPageID)) {
			return false
		}
		if o.AuthoredPageID == "" && o.RouteSeq != 0 {
			return false
		}
		if o.Observed == InteractionOutcomeMet {
			return o.ObservedAt != nil && browserOutcomeTime(*o.ObservedAt) && !o.ObservedAt.After(capturedAt) && o.NavigationSequence > 0
		}
		return o.Reason != ""
	default:
		return false
	}
}

func (o BrowserInteractionOutcome) Clone() BrowserInteractionOutcome {
	out := o
	out.Clock = o.Clock.Clone()
	if o.ObservedAt != nil {
		at := *o.ObservedAt
		out.ObservedAt = &at
	}
	return out
}

// browserInteractionReason admits an interaction's unavailable reason: the
// shared evidence reasons and the interaction ones.
func browserInteractionReason(v string) bool {
	switch v {
	case InteractionUnavailablePageState, InteractionUnavailableChildFrame, InteractionUnavailableSyntheticDispatch, InteractionUnavailableDocumentChanged, InteractionUnavailableTarget,
		InteractionUnavailableCrossOriginAncestry, InteractionUnavailableNonAxisAlignedOr3DTransform, InteractionUnavailableFrameDepthExceeded:
		return true
	}
	return browserEvidenceReason(v)
}

func browserInteractionRole(role string) bool {
	if role == "" || len(role) > 32 {
		return false
	}
	for _, r := range role {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

func browserInteractionToken(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Clone returns a deep copy, or nil when the evidence is malformed.
func (e *BrowserInteractionEvidence) Clone() *BrowserInteractionEvidence {
	if e == nil || (e.Version != BrowserInteractionVersion && !BrowserInteractionHasDispatch(e.Version)) || !BrowserEvidenceGeneration(e.SourceGeneration) || len(e.Interactions) > MaxBrowserInteractionsPerEvidence || e.Dropped < 0 || e.Dropped > 1e12 {
		return nil
	}
	out := *e
	out.Interactions = make([]BrowserInteraction, len(e.Interactions))
	seen := make(map[string]bool, len(e.Interactions))
	for n, i := range e.Interactions {
		if i.Version != e.Version || !i.Valid() || i.SourceGeneration != e.SourceGeneration || seen[i.InteractionID] {
			return nil
		}
		seen[i.InteractionID] = true
		i.Points = append([]BrowserInteractionPoint(nil), i.Points...)
		if i.Target != nil {
			target := *i.Target
			i.Target = &target
		}
		i.Outcome = i.Outcome.Clone()
		if len(i.Outcomes) > 0 {
			outcomes := make([]BrowserInteractionOutcome, len(i.Outcomes))
			for idx, o := range i.Outcomes {
				outcomes[idx] = o.Clone()
			}
			i.Outcomes = outcomes
		}
		if len(i.FrameChain) > 0 {
			chain := make([]BrowserInteractionFrameHop, len(i.FrameChain))
			for idx, hop := range i.FrameChain {
				chain[idx] = hop.Clone()
			}
			i.FrameChain = chain
		}
		i.Execution = i.Execution.Clone()
		i.AuthoredStep = i.AuthoredStep.Clone()
		if i.Dispatch != nil {
			dispatch := *i.Dispatch
			i.Dispatch = &dispatch
		}
		out.Interactions[n] = i
	}
	return &out
}

// SplitBrowserInteractionEvidence returns e as one or more records of the
// same source generation, each within MaxBrowserInteractionEvidenceBytes
// when serialized, in order; Dropped and Incomplete stay on the last. A
// record already within the bound is returned as is. Redaction after
// capture can lengthen a record its capture split to fit.
func SplitBrowserInteractionEvidence(e *BrowserInteractionEvidence) []*BrowserInteractionEvidence {
	if e == nil {
		return nil
	}
	if raw, err := json.Marshal(e); err == nil && len(raw) <= MaxBrowserInteractionEvidenceBytes || len(e.Interactions) < 2 {
		return []*BrowserInteractionEvidence{e}
	}
	envelope, _ := json.Marshal(BrowserInteractionEvidence{Version: e.Version, SourceGeneration: e.SourceGeneration, Interactions: []BrowserInteraction{}, Dropped: e.Dropped, Incomplete: e.Incomplete})
	limit := MaxBrowserInteractionEvidenceBytes - len(envelope)
	var out []*BrowserInteractionEvidence
	size := 0
	for _, interaction := range e.Interactions {
		raw, _ := json.Marshal(interaction)
		encoded := len(raw) + 1 // and its separating comma
		if len(out) == 0 || size+encoded > limit && len(out[len(out)-1].Interactions) > 0 {
			out = append(out, &BrowserInteractionEvidence{Version: e.Version, SourceGeneration: e.SourceGeneration, Interactions: []BrowserInteraction{}})
			size = 0
		}
		last := out[len(out)-1]
		last.Interactions = append(last.Interactions, interaction)
		size += encoded
	}
	out[len(out)-1].Dropped, out[len(out)-1].Incomplete = e.Dropped, e.Incomplete
	return out
}

// DecodeBrowserInteractionEvidence admits one serialized evidence record.
func DecodeBrowserInteractionEvidence(raw []byte) (*BrowserInteractionEvidence, error) {
	if len(raw) > MaxBrowserInteractionEvidenceBytes {
		return nil, errBrowserInteractionMalformed
	}
	var e BrowserInteractionEvidence
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&e); err != nil {
		return nil, errBrowserInteractionMalformed
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errBrowserInteractionMalformed
	}
	cloned := e.Clone()
	if cloned == nil {
		return nil, errBrowserInteractionMalformed
	}
	return cloned, nil
}

// BrowserInteractionEvidenceProvider hands over a session's interactions
// recorded since the last call, one record per source generation.
type BrowserInteractionEvidenceProvider interface {
	TakeBrowserInteractionEvidence() []*BrowserInteractionEvidence
}

var errBrowserInteractionMalformed = errors.New("browser interaction evidence is malformed")

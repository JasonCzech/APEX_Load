package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
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
// page once after the action's own wait and adds no target traffic.
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
	// outcome was waited for.
	InteractionOutcomeNotReached = "not_reached"
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
)

var browserInteractionKinds = map[string]bool{
	"click": true, "dblclick": true, "tap": true, "long_press": true, "swipe": true, "hover": true, "drag": true, "scroll": true,
	"check": true, "key": true, "fill": true, "select": true, "set_files": true,
}

// BrowserInteractionPointerKinds dispatch pointer input; the others are
// keyboard or form input recorded by target, without coordinates.
func BrowserInteractionPointerKind(kind string) bool {
	switch kind {
	case "click", "dblclick", "tap", "long_press", "swipe", "hover", "drag", "check":
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

// BrowserInteractionOutcome is the gesture's declared outcome (the action's
// wait condition) with its deadline, and what was observed.
type BrowserInteractionOutcome struct {
	Declared   string `json:"declared"`
	DeadlineMS int    `json:"deadline_ms,omitempty"`
	Observed   string `json:"observed"`
}

type BrowserInteraction struct {
	Version           int                       `json:"version"`
	SourceGeneration  string                    `json:"source_generation"`
	InteractionID     string                    `json:"interaction_id"`
	ActionID          string                    `json:"action_id"`
	ActionName        string                    `json:"action_name,omitempty"`
	DocumentID        string                    `json:"document_id,omitempty"`
	FrameScope        string                    `json:"frame_scope"`
	Kind              string                    `json:"kind"`
	PointerType       string                    `json:"pointer_type,omitempty"`
	Points            []BrowserInteractionPoint `json:"points,omitempty"`
	Target            *BrowserInteractionTarget `json:"target,omitempty"`
	ViewportWidth     int                       `json:"viewport_width,omitempty"`
	ViewportHeight    int                       `json:"viewport_height,omitempty"`
	ScrollX           float64                   `json:"scroll_x"`
	ScrollY           float64                   `json:"scroll_y"`
	DeviceScaleFactor float64                   `json:"device_scale_factor,omitempty"`
	LayoutRevision    string                    `json:"layout_revision,omitempty"`
	Route             string                    `json:"route,omitempty"`
	Outcome           BrowserInteractionOutcome `json:"outcome"`
	Provenance        string                    `json:"provenance"`
	Success           bool                      `json:"success"`
	CapturedAt        time.Time                 `json:"captured_at"`
	UnavailableReason string                    `json:"unavailable_reason,omitempty"`
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
	if i.Version != BrowserInteractionVersion || !BrowserEvidenceGeneration(i.SourceGeneration) || !browserEvidenceID(i.InteractionID, i.SourceGeneration, false) || !browserEvidenceID(i.ActionID, i.SourceGeneration, true) || !browserEvidenceID(i.DocumentID, i.SourceGeneration, true) || !browserEvidenceText(i.ActionName, 256) || !browserInteractionKinds[i.Kind] {
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
	switch i.Outcome.Observed {
	case InteractionOutcomeNotDeclared:
		if i.Outcome.Declared != "" || i.Outcome.DeadlineMS != 0 {
			return false
		}
	case InteractionOutcomeMet, InteractionOutcomeMissed, InteractionOutcomeNotReached:
		if !browserEvidenceText(i.Outcome.Declared, 64) || i.Outcome.Declared == "" || i.Outcome.DeadlineMS < 0 || i.Outcome.DeadlineMS > 86400000 {
			return false
		}
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

// browserInteractionReason admits an interaction's unavailable reason: the
// shared evidence reasons and the interaction ones.
func browserInteractionReason(v string) bool {
	switch v {
	case InteractionUnavailablePageState, InteractionUnavailableChildFrame, InteractionUnavailableSyntheticDispatch, InteractionUnavailableDocumentChanged, InteractionUnavailableTarget:
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
	if e == nil || e.Version != BrowserInteractionVersion || !BrowserEvidenceGeneration(e.SourceGeneration) || len(e.Interactions) > MaxBrowserInteractionsPerEvidence || e.Dropped < 0 || e.Dropped > 1e12 {
		return nil
	}
	out := *e
	out.Interactions = make([]BrowserInteraction, len(e.Interactions))
	seen := make(map[string]bool, len(e.Interactions))
	for n, i := range e.Interactions {
		if !i.Valid() || i.SourceGeneration != e.SourceGeneration || seen[i.InteractionID] {
			return nil
		}
		seen[i.InteractionID] = true
		i.Points = append([]BrowserInteractionPoint(nil), i.Points...)
		if i.Target != nil {
			target := *i.Target
			i.Target = &target
		}
		out.Interactions[n] = i
	}
	return &out
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

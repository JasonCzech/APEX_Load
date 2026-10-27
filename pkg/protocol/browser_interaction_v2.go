package protocol

import (
	"math"
	"time"
)

// BrowserInteractionVersion2 adds authored identity and independently observed
// dispatch clocks. Its producer must be negotiated with the receiving peer.
// Version 1 remains readable and never acquires inferred dispatch data.
const BrowserInteractionVersion2 = 2

// BrowserInteractionVersion3 adds optional observed outcome clocks.
const BrowserInteractionVersion3 = 3

// BrowserInteractionVersion4 adds multi-outcome binding, authored page identity,
// geometry provenance, and child-frame 2D transform chains.
const BrowserInteractionVersion4 = 4

const (
	MaxBrowserInteractionFrameDepth = 8

	InteractionGeometryProvenanceDispatch   = "dispatch"
	InteractionGeometryProvenancePostAction = "post_action"
)

func BrowserInteractionHasDispatch(version int) bool {
	return version == BrowserInteractionVersion2 || version == BrowserInteractionVersion3 || version == BrowserInteractionVersion4
}

type BrowserInteractionEvidenceV3Provider interface{ SupportsBrowserInteractionEvidenceV3() }

type BrowserInteractionEvidenceV4Provider interface {
	BrowserInteractionEvidenceV3Provider
	SupportsBrowserInteractionEvidenceV4()
}

const (
	InteractionDispatchClockUnavailable    = "clock_unavailable"
	InteractionDispatchDocumentUnavailable = "document_identity_unavailable"
	InteractionDispatchActionUnavailable   = "action_identity_unavailable"
)

// BrowserInteractionFrameHop records the same-task geometry of one ancestor
// iframe element from the target frame outward toward window.top.
type BrowserInteractionFrameHop struct {
	BoundingX        float64   `json:"bounding_x"`
	BoundingY        float64   `json:"bounding_y"`
	BoundingWidth    float64   `json:"bounding_width"`
	BoundingHeight   float64   `json:"bounding_height"`
	OffsetWidth      float64   `json:"offset_width,omitempty"`
	OffsetHeight     float64   `json:"offset_height,omitempty"`
	ClientLeft       float64   `json:"client_left"`
	ClientTop        float64   `json:"client_top"`
	PaddingLeft      float64   `json:"padding_left"`
	PaddingTop       float64   `json:"padding_top"`
	ScrollX          float64   `json:"scroll_x"`
	ScrollY          float64   `json:"scroll_y"`
	VisualViewportX  float64   `json:"visual_viewport_x,omitempty"`
	VisualViewportY  float64   `json:"visual_viewport_y,omitempty"`
	VisualScale      float64   `json:"visual_scale,omitempty"`
	Matrix           []float64 `json:"matrix,omitempty"`
	TransformOriginX float64   `json:"transform_origin_x,omitempty"`
	TransformOriginY float64   `json:"transform_origin_y,omitempty"`
	Non2DTransform   bool      `json:"non_2d_transform,omitempty"`
	CrossOrigin      bool      `json:"cross_origin,omitempty"`
}

func (h BrowserInteractionFrameHop) Clone() BrowserInteractionFrameHop {
	out := h
	if len(h.Matrix) > 0 {
		out.Matrix = append([]float64(nil), h.Matrix...)
	}
	return out
}

func (h BrowserInteractionFrameHop) Valid() bool {
	if h.CrossOrigin {
		return len(h.Matrix) == 0 && !h.Non2DTransform && h.BoundingX == 0 && h.BoundingY == 0 && h.BoundingWidth == 0 && h.BoundingHeight == 0
	}
	if !browserInteractionFinite(h.BoundingX, 1e6) || !browserInteractionFinite(h.BoundingY, 1e6) ||
		!browserInteractionFinite(h.BoundingWidth, 1e6) || h.BoundingWidth < 0 ||
		!browserInteractionFinite(h.BoundingHeight, 1e6) || h.BoundingHeight < 0 ||
		!browserInteractionFinite(h.OffsetWidth, 1e6) || h.OffsetWidth < 0 ||
		!browserInteractionFinite(h.OffsetHeight, 1e6) || h.OffsetHeight < 0 ||
		!browserInteractionFinite(h.ClientLeft, 1e6) || h.ClientLeft < 0 ||
		!browserInteractionFinite(h.ClientTop, 1e6) || h.ClientTop < 0 ||
		!browserInteractionFinite(h.PaddingLeft, 1e6) || h.PaddingLeft < 0 ||
		!browserInteractionFinite(h.PaddingTop, 1e6) || h.PaddingTop < 0 ||
		!browserInteractionFinite(h.ScrollX, 1e9) || !browserInteractionFinite(h.ScrollY, 1e9) ||
		!browserInteractionFinite(h.VisualViewportX, 1e6) || !browserInteractionFinite(h.VisualViewportY, 1e6) ||
		!browserInteractionFinite(h.VisualScale, 100) || h.VisualScale < 0 ||
		!browserInteractionFinite(h.TransformOriginX, 1e6) || !browserInteractionFinite(h.TransformOriginY, 1e6) {
		return false
	}
	if len(h.Matrix) != 0 && len(h.Matrix) != 6 {
		return false
	}
	for _, v := range h.Matrix {
		if !browserInteractionFinite(v, 1e6) {
			return false
		}
	}
	return true
}

func (h BrowserInteractionFrameHop) Invertible2D() bool {
	if h.CrossOrigin || h.Non2DTransform || !h.Valid() {
		return false
	}
	if len(h.Matrix) == 0 {
		return true
	}
	det := h.Matrix[0]*h.Matrix[3] - h.Matrix[1]*h.Matrix[2]
	return math.Abs(det) >= 1e-6
}

// ApplyToPoint maps a point from the child frame's viewport CSS space into its
// immediate parent frame's viewport CSS space.
func (h BrowserInteractionFrameHop) ApplyToPoint(p BrowserInteractionPoint) (BrowserInteractionPoint, bool) {
	if !h.Invertible2D() {
		return BrowserInteractionPoint{}, false
	}
	px := h.ClientLeft + h.PaddingLeft + p.X
	py := h.ClientTop + h.PaddingTop + p.Y
	a, b, c, d := 1.0, 0.0, 0.0, 1.0
	if len(h.Matrix) == 6 {
		a, b, c, d = h.Matrix[0], h.Matrix[1], h.Matrix[2], h.Matrix[3]
	}
	w, height := h.OffsetWidth, h.OffsetHeight
	if w <= 0 {
		w = h.BoundingWidth
	}
	if height <= 0 {
		height = h.BoundingHeight
	}
	minX := min(0.0, a*w) + min(0.0, c*height)
	minY := min(0.0, b*w) + min(0.0, d*height)
	out := BrowserInteractionPoint{
		X: h.BoundingX - minX + a*px + c*py,
		Y: h.BoundingY - minY + b*px + d*py,
	}
	if !browserInteractionFinite(out.X, 1e6) || !browserInteractionFinite(out.Y, 1e6) {
		return BrowserInteractionPoint{}, false
	}
	return out, true
}

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
	if i.Version < BrowserInteractionVersion4 && (i.GeometryProvenance != "" || i.AuthoredPageID != "" || i.RouteSeq != 0 || len(i.FrameChain) != 0) {
		return false
	}
	if i.Version == BrowserInteractionVersion {
		return i.Execution == nil && i.AuthoredStep == nil && i.Dispatch == nil && i.DispatchUnavailableReason == ""
	}
	if i.Execution != nil && (!i.Execution.Valid() || i.AuthoredStep == nil) {
		return false
	}
	if !BrowserInteractionHasDispatch(i.Version) || i.AuthoredStep != nil && !i.AuthoredStep.Valid() {
		return false
	}
	if i.Version == BrowserInteractionVersion4 {
		if i.GeometryProvenance != InteractionGeometryProvenanceDispatch && i.GeometryProvenance != InteractionGeometryProvenancePostAction {
			return false
		}
		if i.AuthoredPageID != "" && (len(i.AuthoredPageID) != 64 || !browserInteractionToken(i.AuthoredPageID)) {
			return false
		}
		if i.AuthoredPageID == "" && i.RouteSeq != 0 || i.RouteSeq > 9007199254740991 {
			return false
		}
		if i.FrameScope == "main_frame" && len(i.FrameChain) != 0 {
			return false
		}
		if len(i.FrameChain) > MaxBrowserInteractionFrameDepth {
			return false
		}
		for _, hop := range i.FrameChain {
			if !hop.Valid() {
				return false
			}
		}
	}
	if i.Dispatch != nil {
		if i.DispatchUnavailableReason != "" || !i.Dispatch.Valid(i.SourceGeneration) || i.DocumentID != i.Dispatch.DocumentID {
			return false
		}
		if i.Version < BrowserInteractionVersion4 {
			return i.FrameScope == "main_frame"
		}
		if i.GeometryProvenance != InteractionGeometryProvenanceDispatch {
			return false
		}
		if i.FrameScope == "child_frame" {
			if len(i.FrameChain) == 0 {
				return false
			}
			for _, hop := range i.FrameChain {
				if !hop.Invertible2D() {
					return false
				}
			}
		}
		return true
	}
	switch i.DispatchUnavailableReason {
	case "not_observed", InteractionDispatchClockUnavailable, InteractionDispatchDocumentUnavailable, InteractionDispatchActionUnavailable, InteractionUnavailablePageState, InteractionUnavailableChildFrame,
		InteractionUnavailableSyntheticDispatch, InteractionUnavailableDocumentChanged,
		InteractionUnavailableCrossOriginAncestry, InteractionUnavailableNonAxisAlignedOr3DTransform, InteractionUnavailableFrameDepthExceeded:
		return i.Version == BrowserInteractionVersion4 || (i.DispatchUnavailableReason != InteractionUnavailableCrossOriginAncestry && i.DispatchUnavailableReason != InteractionUnavailableNonAxisAlignedOr3DTransform && i.DispatchUnavailableReason != InteractionUnavailableFrameDepthExceeded)
	}
	return false
}

// BrowserInteractionEvidenceV2Provider guarantees that the runtime can emit
// either v1 or explicitly selected v2. It is capability, not capture enablement.
type BrowserInteractionEvidenceV2Provider interface {
	SupportsBrowserInteractionEvidenceV2()
}

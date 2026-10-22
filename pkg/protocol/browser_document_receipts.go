package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"time"
)

const BrowserDocumentReceiptVersion = 1
const BrowserDocumentReceiptLifetimeVersion = 2
const MaxBrowserDocumentReceipts = (MaxBrowserDocumentObservations / 5) * 7
const MaxBrowserDocumentReceiptRecords = 2_000_000
const MaxBrowserDocumentReceiptBytes = 512 << 10

// BrowserDocumentReceipt is a one-way delivery observation, not a clock
// synchronization sample. Browser offsets are supplied by the target's
// document binding; host timestamps are sampled by this native process only.
// No maximum delivery delay, browser clock integrity or symmetric latency is
// implied. MeasurementID/Revision bind metric receipts to immutable raw data.
type BrowserDocumentReceipt struct {
	DocumentID          string    `json:"document_id"`
	LifecycleID         string    `json:"lifecycle_id"`
	FrameID             string    `json:"frame_id"`
	Kind                string    `json:"kind"`
	MeasurementID       string    `json:"measurement_id,omitempty"`
	Revision            uint64    `json:"revision,omitempty"`
	BrowserTimeOriginMS float64   `json:"browser_time_origin_ms"`
	BrowserOffsetMS     float64   `json:"browser_offset_ms"`
	ReceivedAt          time.Time `json:"received_at"`
	HostElapsedNS       int64     `json:"host_elapsed_ns"`
	WallDriftNS         int64     `json:"wall_drift_ns"`
}

// BrowserDocumentReceiptEvidence is a bounded acquisition
// snapshot. It is deliberately separate from the v2 document wire contract:
// callers must not silently add it to archives read by v2-only peers. Durable publication and source closure are separate contracts. A missing terminal receipt may mean
// cancellation, a lost binding, or a silent source; this is NOT an expected
// source ledger, and no count of these receipts proves population coverage.
//
// SourceGeneration identifies the monotonic origin StartedAt. HostElapsedNS
// values can be compared only within that generation. WallStepObserved latches
// a sampled absolute wall/monotonic divergence greater than 100 ms. False does
// not prove clock continuity between samples, bound drift, or qualify a map
// to the controller clock. Raw browser measurement windows stay unchanged.
type BrowserDocumentReceiptEvidence struct {
	Version          int                      `json:"version"`
	SourceGeneration string                   `json:"source_generation"`
	StartedAt        time.Time                `json:"started_at"`
	WallStepObserved bool                     `json:"wall_step_observed"`
	Incomplete       bool                     `json:"incomplete"`
	Receipts         []BrowserDocumentReceipt `json:"receipts"`
	Lifetime         *BrowserNativeLifetime   `json:"lifetime,omitempty"`
}

// BrowserDocumentReceiptEvidenceProvider returns a bounded local snapshot;
// callers must bind it to their own execution slot and durable publication.
type BrowserDocumentReceiptEvidenceProvider interface {
	BrowserDocumentReceiptEvidence() *BrowserDocumentReceiptEvidence
}

func (e *BrowserDocumentReceiptEvidence) Clone() *BrowserDocumentReceiptEvidence {
	if e == nil || (e.Version != BrowserDocumentReceiptVersion && e.Version != BrowserDocumentReceiptLifetimeVersion) || e.Version == 1 && e.Lifetime != nil || !BrowserEvidenceGeneration(e.SourceGeneration) || e.StartedAt.IsZero() || e.StartedAt.Location() != time.UTC || len(e.Receipts) > MaxBrowserDocumentReceipts {
		return nil
	}
	out := *e
	if e.Lifetime != nil {
		if !e.Lifetime.CreationBracketed || e.Lifetime.EndNS < 0 || !e.Lifetime.Closed && e.Lifetime.EndNS != 0 {
			return nil
		}
		lifetime := *e.Lifetime
		out.Lifetime = &lifetime
	}
	out.Receipts = append([]BrowserDocumentReceipt(nil), e.Receipts...)
	seen := make(map[string]bool, len(e.Receipts))
	type lifecycleIdentity struct {
		document, frame string
		origin          float64
	}
	lifecycles := make(map[string]lifecycleIdentity)
	documents := make(map[string]lifecycleIdentity)
	for _, r := range e.Receipts {
		if e.Lifetime != nil && e.Lifetime.Closed && r.HostElapsedNS > e.Lifetime.EndNS {
			return nil
		}
		if !browserEvidenceID(r.DocumentID, e.SourceGeneration, false) || !browserEvidenceID(r.LifecycleID, e.SourceGeneration, false) || !browserEvidenceID(r.FrameID, e.SourceGeneration, false) || r.ReceivedAt.IsZero() || r.ReceivedAt.Location() != time.UTC || r.HostElapsedNS < 0 || math.IsNaN(r.BrowserTimeOriginMS) || math.IsInf(r.BrowserTimeOriginMS, 0) || r.BrowserTimeOriginMS <= 0 || r.BrowserTimeOriginMS > 253402300799999 || !browserFinite(r.BrowserOffsetMS, 86400000) {
			return nil
		}
		if !e.StartedAt.Add(time.Duration(r.HostElapsedNS)).Add(time.Duration(r.WallDriftNS)).Equal(r.ReceivedAt) {
			return nil
		}
		if (r.WallDriftNS > int64(100*time.Millisecond) || r.WallDriftNS < -int64(100*time.Millisecond)) && !e.WallStepObserved {
			return nil
		}
		switch r.Kind {
		case "begin", "end":
			if r.MeasurementID != "" || r.Revision != 0 {
				return nil
			}
		case "metric":
			prefix := r.DocumentID + "/" + r.LifecycleID + "/"
			if !strings.HasPrefix(r.MeasurementID, prefix) || browserMetricUnit(strings.TrimPrefix(r.MeasurementID, prefix)) == "" || r.Revision < 1 {
				return nil
			}
		default:
			return nil
		}
		identity := lifecycleIdentity{r.DocumentID, r.FrameID, r.BrowserTimeOriginMS}
		if prior, ok := lifecycles[r.LifecycleID]; ok && prior != identity {
			return nil
		}
		if prior, ok := documents[r.DocumentID]; ok && prior != identity {
			return nil
		}
		lifecycles[r.LifecycleID] = identity
		documents[r.DocumentID] = identity
		key := r.LifecycleID + "/" + r.Kind + "/" + r.MeasurementID
		if seen[key] {
			return nil
		}
		seen[key] = true
	}
	return &out
}

func DecodeBrowserDocumentReceiptEvidence(raw []byte) (*BrowserDocumentReceiptEvidence, error) {
	if len(raw) == 0 || len(raw) > MaxBrowserDocumentReceiptBytes || !browserDocumentWirePresence(raw, reflect.TypeOf(BrowserDocumentReceiptEvidence{}), MaxBrowserDocumentReceiptBytes) {
		return nil, fmt.Errorf("browser document receipt evidence is malformed or exceeds its bounds")
	}
	var value BrowserDocumentReceiptEvidence
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil {
		return nil, fmt.Errorf("browser document receipt evidence is malformed")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, fmt.Errorf("browser document receipt evidence has trailing data")
	}
	owned := value.Clone()
	if owned == nil {
		return nil, fmt.Errorf("browser document receipt evidence is malformed")
	}
	return owned, nil
}

// BrowserDocumentReceiptProjection preserves strict v1 wire compatibility.
// Version 1 intentionally carries no lifetime/qualification proof.
func BrowserDocumentReceiptProjection(e *BrowserDocumentReceiptEvidence, version int) *BrowserDocumentReceiptEvidence {
	e = e.Clone()
	if e == nil || version < 1 || version > 2 || e.Version < version {
		return nil
	}
	e.Version = version
	if version == 1 {
		e.Lifetime = nil
	}
	return e
}

package protocol

import (
	"fmt"
	"math"
	"time"
)

// BrowserClockDomain identifies one monotonic authority incarnation. Elapsed
// values from another process or a restarted controller are never comparable.
// RunID is an opaque retained run identity, not an origin or credential.
// BrowserNativeLifetime is acquired by the native context owner. Zero in its
// source monotonic domain is sampled BEFORE NewContext, never at JS bootstrap.
// EndNS is frozen only after native close and all owned callbacks join.
type BrowserNativeLifetime struct {
	CreationBracketed bool  `json:"creation_bracketed"`
	Closed            bool  `json:"closed"`
	EndNS             int64 `json:"end_ns"`
}

type BrowserClockDomain struct {
	RunID       string `json:"run_id"`
	Incarnation string `json:"incarnation"`
	DomainID    string `json:"domain_id"`
}

func (d BrowserClockDomain) Valid() bool {
	return len(d.RunID) > 0 && len(d.RunID) <= 256 && BrowserEvidenceGeneration(d.Incarnation) && BrowserEvidenceGeneration(d.DomainID)
}

// BrowserCausalEnvelope brackets an ENTIRE native context lifetime. The
// authority samples BeforeNS before dispatch/creation, and AfterNS only after
// terminal acknowledgement. SourceUpperNS comes from the source's monotonic
// clock, after all native document callbacks have joined. These are causal
// inequalities, not an offset/drift estimate. No interpolation or wall-clock
// arithmetic can narrow them. Only the trusted dispatcher may create this DTO.
type BrowserCausalEnvelope struct {
	Version                 int                `json:"version"`
	Authority               BrowserClockDomain `json:"authority"`
	SourceGeneration        string             `json:"source_generation"`
	BeforeNS                int64              `json:"before_ns"`
	AfterNS                 int64              `json:"after_ns"`
	SourceUpperNS           int64              `json:"source_upper_ns"`
	NativeCreationBracketed bool               `json:"native_creation_bracketed"`
	TerminalAcknowledged    bool               `json:"terminal_acknowledged"`
}

type BrowserMonotonicSpan struct {
	Authority BrowserClockDomain `json:"authority"`
	StartNS   int64              `json:"start_ns"`
	EndNS     int64              `json:"end_ns"`
}

// MapBrowserDocumentLifetime returns a conservative whole-lifetime window,
// bound to an exact immutable metric revision. It does not certify source
// coverage. Unobserved sources, failed publication and expected native loader
// reconciliation are independently checked by the closure ledger.
func MapBrowserDocumentLifetime(e *BrowserDocumentReceiptEvidence, o BrowserDocumentObservation, envelope BrowserCausalEnvelope) (BrowserMonotonicSpan, error) {
	unavailable := func(reason string) (BrowserMonotonicSpan, error) {
		return BrowserMonotonicSpan{}, fmt.Errorf("browser clock mapping unavailable: %s", reason)
	}
	e = e.Clone()
	if e == nil || e.Incomplete || e.Lifetime == nil || !e.Lifetime.CreationBracketed || !e.Lifetime.Closed || e.Lifetime.EndNS != envelope.SourceUpperNS || envelope.Version != 1 || !envelope.Authority.Valid() || envelope.SourceGeneration != e.SourceGeneration || envelope.SourceGeneration != o.SourceGeneration || !envelope.NativeCreationBracketed || !envelope.TerminalAcknowledged || envelope.BeforeNS < 0 || envelope.AfterNS < envelope.BeforeNS || envelope.SourceUpperNS < 0 {
		return unavailable("incomplete or foreign lifetime")
	}
	if o.State != "final" || o.Revision == 0 {
		return unavailable("nonterminal measurement")
	}
	var begin, end, metric *BrowserDocumentReceipt
	for i := range e.Receipts {
		r := &e.Receipts[i]
		if r.HostElapsedNS > envelope.SourceUpperNS {
			return unavailable("receipt after terminal acknowledgement")
		}
		if r.LifecycleID != o.LifecycleID {
			continue
		}
		if r.DocumentID != o.DocumentID || r.FrameID != o.FrameID {
			return unavailable("conflicting document identity")
		}
		switch r.Kind {
		case "begin":
			begin = r
		case "end":
			end = r
		case "metric":
			if r.MeasurementID == o.MeasurementID {
				metric = r
			}
		}
	}
	if begin == nil || end == nil || metric == nil || metric.Revision != o.Revision {
		return unavailable("missing lifecycle or metric receipt")
	}
	if begin.HostElapsedNS > metric.HostElapsedNS || metric.HostElapsedNS > end.HostElapsedNS || begin.BrowserOffsetMS > metric.BrowserOffsetMS || metric.BrowserOffsetMS > end.BrowserOffsetMS {
		return unavailable("unordered lifecycle")
	}
	if o.MeasurementID != o.DocumentID+"/"+o.LifecycleID+"/"+o.Metric || !o.CapturedAt.Equal(metric.ReceivedAt) {
		return unavailable("raw measurement receipt mismatch")
	}
	startMS := begin.BrowserTimeOriginMS
	switch o.LifecyclePhase {
	case "navigation":
	case "bfcache_restore":
		startMS += begin.BrowserOffsetMS
	default:
		return unavailable("unsupported lifecycle phase")
	}
	startTime := time.Unix(int64(startMS/1000), int64(math.Mod(startMS, 1000)*1e6)).UTC()
	if !o.WindowStart.Equal(startTime) {
		return unavailable("raw measurement start mismatch")
	}
	endMS := metric.BrowserTimeOriginMS + metric.BrowserOffsetMS
	endTime := time.Unix(int64(endMS/1000), int64(math.Mod(endMS, 1000)*1e6)).UTC()
	if !o.WindowEnd.Equal(endTime) || o.WindowStart.IsZero() || o.WindowStart.After(o.WindowEnd) {
		return unavailable("raw measurement window mismatch")
	}
	return BrowserMonotonicSpan{Authority: envelope.Authority, StartNS: envelope.BeforeNS, EndNS: envelope.AfterNS}, nil
}

// ContainedInBrowserSteadySpan requires a SINGLE positively established span
// in the same authority domain. Adjacent/equal-load spans are not merged across
// pauses, unknown intervals or restarts. Bounds are closed at both ends.
func ContainedInBrowserSteadySpan(window BrowserMonotonicSpan, spans []BrowserMonotonicSpan) bool {
	if !window.Authority.Valid() || window.StartNS < 0 || window.EndNS < window.StartNS {
		return false
	}
	for _, span := range spans {
		if span.Authority == window.Authority && span.StartNS >= 0 && span.EndNS > span.StartNS && window.StartNS >= span.StartNS && window.EndNS <= span.EndNS {
			return true
		}
	}
	return false
}

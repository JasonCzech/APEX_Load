package protocol

import (
	"errors"
	"fmt"
	"math"
	"time"
)

const MaxBrowserProofRetainedContexts = 4096

type BrowserSteadyProof struct {
	BrowserMonotonicSpan
	VUsers int
}
type BrowserContextProof struct {
	Receipts          *BrowserDocumentReceiptEvidence
	Authority         BrowserClockDomain
	SourceOriginNS    int64
	BeforeNS, AfterNS int64
}
type browserProofOwner struct {
	identity        string
	authority       BrowserClockDomain
	sequence, total uint64
	at              int64
	sealed, failed  bool
	slots           map[string]map[string]bool
	seenSlots       map[string]bool
	contexts        map[string]*BrowserContextProof
	steady          *BrowserSteadyProof
	spans           []BrowserSteadyProof
}

// BrowserSourceReconciler consumes the retained journal in exact owner order.
// expectedOwners must come from the controller's pre-dispatch durable intent;
// passing the owners found in this stream would not establish source closure.
type BrowserSourceReconciler struct {
	runID         string
	expected      map[string]bool
	owners        map[string]*browserProofOwner
	generations   map[string]*browserProofOwner
	observed      map[string]map[string]BrowserDocumentReceipt
	observedCount int
	failed        error
}

func NewBrowserSourceReconciler(runID string, expectedOwners []string) *BrowserSourceReconciler {
	r := &BrowserSourceReconciler{runID: runID, expected: map[string]bool{}, owners: map[string]*browserProofOwner{}, generations: map[string]*browserProofOwner{}, observed: map[string]map[string]BrowserDocumentReceipt{}}
	if runID == "" || len(expectedOwners) == 0 || len(expectedOwners) > MaxBrowserProofActiveSlots {
		r.failed = errors.New("expected browser source inventory unavailable")
	}
	for _, id := range expectedOwners {
		if id == "" || len(id) > 256 || r.expected[id] {
			r.failed = errors.New("invalid expected browser source inventory")
		}
		r.expected[id] = true
	}
	return r
}
func (r *BrowserSourceReconciler) Observe(e *BrowserDocumentReceiptEvidence) error {
	if r.failed != nil {
		return r.failed
	}
	fail := func(reason string) error {
		r.failed = fmt.Errorf("browser source closure unavailable: %s", reason)
		return r.failed
	}
	e = e.Clone()
	if e == nil {
		return fail("malformed receipt")
	}
	if e.Source == nil {
		seen := r.observed[e.SourceGeneration]
		if seen == nil {
			if len(r.observed) >= MaxBrowserProofRetainedContexts {
				return fail("unbound snapshot budget exceeded")
			}
			seen = map[string]BrowserDocumentReceipt{}
			r.observed[e.SourceGeneration] = seen
		}
		for _, receipt := range e.Receipts {
			key := receipt.LifecycleID + "/" + receipt.Kind + "/" + receipt.MeasurementID
			if prior, ok := seen[key]; ok {
				if prior != receipt {
					return fail("conflicting snapshot receipt")
				}
			} else {
				if r.observedCount >= 65536 {
					return fail("snapshot receipt budget exceeded")
				}
				r.observedCount++
				seen[key] = receipt
			}
		}
		return nil
	}
	event := e.Source
	if event.Kind != "context_expected" && event.Kind != "context_terminal" && (e.SourceGeneration != event.Authority.DomainID || len(e.Receipts) != 0 || e.Lifetime != nil || e.Closure != nil || event.SourceOriginNS != 0) {
		return fail("noncanonical control event")
	}
	if event.Authority.RunID != r.runID || !r.expected[event.OwnerID] {
		return fail("foreign source")
	}
	owner := r.owners[event.OwnerID]
	if owner == nil {
		if event.Kind != "owner_expected" || event.Sequence != 1 || event.ExpectedSlots != 0 || event.SlotID != "" || e.SourceGeneration != event.Authority.DomainID {
			return fail("missing owner expectation")
		}
		owner = &browserProofOwner{identity: event.OwnerID, authority: event.Authority, slots: map[string]map[string]bool{}, seenSlots: map[string]bool{}, contexts: map[string]*BrowserContextProof{}}
		r.owners[event.OwnerID] = owner
	} else if event.Kind == "owner_expected" {
		return fail("duplicate owner")
	}
	if owner.sealed || event.Authority != owner.authority || event.Sequence != owner.sequence+1 || event.AtNS < owner.at {
		return fail("reordered or restarted source")
	}
	owner.sequence = event.Sequence
	owner.at = event.AtNS
	if event.Kind == "slot_expected" {
		owner.total++
	}
	if event.ExpectedSlots != owner.total {
		return fail("expected source count conflict")
	}
	owner.failed = owner.failed || !event.Successful
	switch event.Kind {
	case "owner_expected":
	case "slot_expected":
		if owner.seenSlots[event.SlotID] || len(owner.seenSlots) >= MaxBrowserProofRetainedContexts || len(owner.slots) >= MaxBrowserProofActiveSlots {
			return fail("duplicate or excessive execution slot")
		}
		owner.seenSlots[event.SlotID] = true
		owner.slots[event.SlotID] = map[string]bool{}
	case "context_expected":
		slots, ok := owner.slots[event.SlotID]
		if !ok || r.generations[e.SourceGeneration] != nil || len(r.generations) >= MaxBrowserProofRetainedContexts {
			return fail("unexpected context")
		}
		if e.Lifetime == nil || !e.Lifetime.CreationBracketed || e.Lifetime.Closed || len(e.Receipts) > 0 {
			return fail("late context expectation")
		}
		slots[e.SourceGeneration] = false
		owner.contexts[e.SourceGeneration] = &BrowserContextProof{Receipts: e, Authority: owner.authority, BeforeNS: event.AtNS, SourceOriginNS: event.SourceOriginNS}
		r.generations[e.SourceGeneration] = owner
	case "context_terminal":
		slots, ok := owner.slots[event.SlotID]
		prior := owner.contexts[e.SourceGeneration]
		if !ok || prior == nil {
			return fail("terminal context without expectation")
		}
		closed, expected := slots[e.SourceGeneration]
		if !expected || closed {
			return fail("duplicate terminal context")
		}
		if !e.StartedAt.Equal(prior.Receipts.StartedAt) || event.SourceOriginNS != prior.SourceOriginNS {
			return fail("context origin changed")
		}
		if e.Lifetime == nil || !e.Lifetime.Closed || e.Closure == nil || !e.Closure.DiscoveryComplete || len(e.Closure.Documents) == 0 || e.Lifetime.EndNS > event.AtNS-event.SourceOriginNS {
			owner.failed = true
		}
		if e.Closure != nil {
			seen := map[string]string{}
			for _, doc := range e.Closure.Documents {
				if !doc.Terminal || doc.DocumentID == "" || seen[doc.LifecycleID] != "" || e.Lifetime == nil || doc.LowerNS > e.Lifetime.EndNS {
					owner.failed = true
				}
				seen[doc.LifecycleID] = doc.DocumentID
			}
			for _, receipt := range e.Receipts {
				if seen[receipt.LifecycleID] != receipt.DocumentID {
					owner.failed = true
				}
			}
		}
		prior.Receipts = e
		prior.AfterNS = event.AtNS
		slots[e.SourceGeneration] = true
	case "slot_terminal":
		contexts, ok := owner.slots[event.SlotID]
		if !ok {
			return fail("unexpected terminal slot")
		}
		if len(contexts) == 0 {
			owner.failed = true
		}
		for _, closed := range contexts {
			owner.failed = owner.failed || !closed
		}
		delete(owner.slots, event.SlotID)
	case "steady", "unknown":
		if owner.steady != nil {
			owner.steady.EndNS = event.AtNS
			if owner.steady.EndNS > owner.steady.StartNS {
				owner.spans = append(owner.spans, *owner.steady)
			}
			owner.steady = nil
		}
		if event.Kind == "steady" && event.VUsers > 0 {
			owner.steady = &BrowserSteadyProof{BrowserMonotonicSpan: BrowserMonotonicSpan{Authority: owner.authority, StartNS: event.AtNS}, VUsers: event.VUsers}
		}
	case "owner_terminal":
		if len(owner.slots) > 0 || owner.total == 0 {
			owner.failed = true
		}
		owner.sealed = true
		if owner.steady != nil {
			owner.steady.EndNS = event.AtNS
			owner.spans = append(owner.spans, *owner.steady)
			owner.steady = nil
		}
	default:
		return fail("unsupported source event")
	}
	return nil
}
func (r *BrowserSourceReconciler) Complete() bool {
	if r == nil || r.failed != nil || len(r.owners) != len(r.expected) {
		return false
	}
	for generation, receipts := range r.observed {
		owner := r.generations[generation]
		if owner == nil || owner.contexts[generation] == nil {
			return false
		}
		terminal := owner.contexts[generation].Receipts
		if terminal == nil {
			return false
		}
		retained := map[string]BrowserDocumentReceipt{}
		for _, receipt := range terminal.Receipts {
			retained[receipt.LifecycleID+"/"+receipt.Kind+"/"+receipt.MeasurementID] = receipt
		}
		for key, receipt := range receipts {
			if final, ok := retained[key]; !ok || final != receipt {
				return false
			}
		}
	}
	for _, owner := range r.owners {
		if !owner.sealed || owner.failed {
			return false
		}
	}
	return true
}
func (r *BrowserSourceReconciler) Context(generation string) (*BrowserContextProof, []BrowserSteadyProof, bool) {
	if !r.Complete() {
		return nil, nil, false
	}
	owner := r.generations[generation]
	if owner == nil {
		return nil, nil, false
	}
	p := owner.contexts[generation]
	if p == nil {
		return nil, nil, false
	}
	copy := *p
	copy.Receipts = p.Receipts.Clone()
	return &copy, append([]BrowserSteadyProof(nil), owner.spans...), true
}

// Map returns a conservative bound for the complete measurement window in the
// execution owner's monotonic domain. The source origin was sampled in this
// same process before native context creation; no wall-clock offset is used.
// A native command/commit join may narrow the creation bound. The receipt only
// supplies an upper bound, never an estimate of the browser event time.
func (r *BrowserSourceReconciler) Map(o BrowserDocumentObservation) (BrowserMonotonicSpan, []BrowserSteadyProof, error) {
	p, spans, ok := r.Context(o.SourceGeneration)
	if !ok {
		return BrowserMonotonicSpan{}, nil, errors.New("browser source closure unavailable")
	}
	e := p.Receipts
	if o.State != "final" || o.LifecyclePhase != "navigation" || o.Revision == 0 || e == nil || e.Lifetime == nil || e.Closure == nil {
		return BrowserMonotonicSpan{}, nil, errors.New("browser measurement not mappable")
	}
	var lower int64
	found := false
	for _, d := range e.Closure.Documents {
		if d.DocumentID == o.DocumentID && d.LifecycleID == o.LifecycleID && d.Terminal {
			lower = d.LowerNS
			found = true
		}
	}
	var begin, metric *BrowserDocumentReceipt
	for i := range e.Receipts {
		q := &e.Receipts[i]
		if q.LifecycleID != o.LifecycleID {
			continue
		}
		if q.DocumentID != o.DocumentID || q.FrameID != o.FrameID {
			return BrowserMonotonicSpan{}, nil, errors.New("browser measurement identity conflict")
		}
		if q.Kind == "begin" {
			begin = q
		}
		if q.Kind == "metric" && q.MeasurementID == o.MeasurementID {
			metric = q
		}
	}
	if !found || begin == nil || metric == nil || metric.Revision != o.Revision || begin.HostElapsedNS > metric.HostElapsedNS || begin.BrowserOffsetMS > metric.BrowserOffsetMS || lower > begin.HostElapsedNS || o.MeasurementID != o.DocumentID+"/"+o.LifecycleID+"/"+o.Metric || !o.CapturedAt.Equal(metric.ReceivedAt) {
		return BrowserMonotonicSpan{}, nil, errors.New("browser measurement receipt mismatch")
	}
	startMS := begin.BrowserTimeOriginMS
	endMS := metric.BrowserTimeOriginMS + metric.BrowserOffsetMS
	rawTime := func(ms float64) time.Time { return time.Unix(int64(ms/1000), int64(math.Mod(ms, 1000)*1e6)).UTC() }
	if !o.WindowStart.Equal(rawTime(startMS)) || !o.WindowEnd.Equal(rawTime(endMS)) || o.WindowStart.After(o.WindowEnd) {
		return BrowserMonotonicSpan{}, nil, errors.New("browser measurement raw window mismatch")
	}
	// Reconciliation established EndNS <= AfterNS-origin, so these additions
	// cannot overflow and every returned bound precedes terminal acknowledgement.
	return BrowserMonotonicSpan{Authority: p.Authority, StartNS: p.SourceOriginNS + lower, EndNS: p.SourceOriginNS + metric.HostElapsedNS}, spans, nil
}

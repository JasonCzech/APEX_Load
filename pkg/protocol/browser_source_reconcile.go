package protocol

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

const MaxBrowserProofRetainedContexts = 4096

type BrowserSteadyProof struct {
	BrowserMonotonicSpan
	VUsers int
}
type BrowserContextProof struct {
	AuthorityStartedAt time.Time
	Receipts           *BrowserDocumentReceiptEvidence
	Authority          BrowserClockDomain
	SourceOriginNS     int64
	BeforeNS, AfterNS  int64
}
type browserProofOwner struct {
	startedAt       time.Time
	identity        string
	authority       BrowserClockDomain
	sequence, total uint64
	at              int64
	firstActivityNS int64
	hasActivity     bool
	sealed, failed  bool
	slots           map[string]map[string]bool
	seenSlots       map[string]bool
	contexts        map[string]*BrowserContextProof
	steady          *BrowserSteadyProof
	spans           []BrowserSteadyProof
}

func (o *browserProofOwner) recordActivity(ns int64) {
	if !o.hasActivity || ns < o.firstActivityNS {
		o.firstActivityNS = ns
		o.hasActivity = true
	}
}

// BrowserSourceReconciler consumes the retained journal in exact owner order.
// expectedOwners must come from the controller's pre-dispatch durable intent;
// passing the owners found in this stream would not establish source closure.
type BrowserSourceReconciler struct {
	runID             string
	expected          map[string]bool
	owners            map[string]*browserProofOwner
	generations       map[string]*browserProofOwner
	observed          map[string]map[string]BrowserDocumentReceipt
	observedCount     int
	clockEnvelope     *BrowserDistributedClockEnvelope
	checked, complete bool
	failed            error
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
	r.checked = false
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
		owner = &browserProofOwner{startedAt: e.StartedAt, identity: event.OwnerID, authority: event.Authority, slots: map[string]map[string]bool{}, seenSlots: map[string]bool{}, contexts: map[string]*BrowserContextProof{}}
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
		owner.recordActivity(event.AtNS)
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
		if event.SourceOriginNS > 0 {
			owner.recordActivity(min(event.AtNS, event.SourceOriginNS))
		} else {
			owner.recordActivity(event.AtNS)
		}
		slots[e.SourceGeneration] = false
		owner.contexts[e.SourceGeneration] = &BrowserContextProof{AuthorityStartedAt: owner.startedAt, Receipts: e, Authority: owner.authority, BeforeNS: event.AtNS, SourceOriginNS: event.SourceOriginNS}
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
			owner.recordActivity(event.AtNS)
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
func (r *BrowserSourceReconciler) Complete() (complete bool) {
	if r == nil {
		return false
	}
	if r.checked {
		return r.complete
	}
	defer func() { r.checked = true; r.complete = complete }()
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

func (r *BrowserSourceReconciler) SetDistributedClockEnvelope(env *BrowserDistributedClockEnvelope) {
	if r == nil {
		return
	}
	r.clockEnvelope = env.Clone()
}

func (r *BrowserSourceReconciler) distributedClockParams() (maxSkewPPM, maxRTT, maxGap int64, ok bool) {
	if r == nil || r.clockEnvelope == nil {
		return 0, 0, 0, false
	}
	env := r.clockEnvelope
	if env.Version != 1 || !env.Controller.Valid() || env.Controller.RunID != r.runID || env.AuthorityStartedAt.IsZero() {
		return 0, 0, 0, false
	}
	maxSkewPPM = env.MaxSkewPPM
	if maxSkewPPM == 0 {
		maxSkewPPM = DefaultBrowserClockMaxSkewPPM
	}
	if maxSkewPPM < 0 || maxSkewPPM > MaxBrowserClockMaxSkewPPM {
		return 0, 0, 0, false
	}
	maxRTT = env.MaxBracketRTTNS
	if maxRTT == 0 {
		maxRTT = DefaultBrowserClockMaxBracketRTTNS
	}
	if maxRTT <= 0 || maxRTT > DefaultBrowserClockMaxBracketRTTNS {
		return 0, 0, 0, false
	}
	maxGap = env.MaxBracketGapNS
	if maxGap == 0 {
		maxGap = DefaultBrowserClockMaxBracketGapNS
	}
	if maxGap <= 0 || maxGap > MaxBrowserClockMaxBracketGapNS {
		return 0, 0, 0, false
	}
	return maxSkewPPM, maxRTT, maxGap, true
}

func projectDistributedOwnerReading(ownerAuthority BrowserClockDomain, brackets []BrowserClockBracket, maxSkewPPM, g int64) (low, high int64, ok bool) {
	if len(brackets) == 0 || g < 0 {
		return 0, 0, false
	}
	low = 0
	high = math.MaxInt64
	for i := range brackets {
		b := brackets[i]
		if b.Authority != ownerAuthority || b.ControllerSendNS < 0 || b.ControllerRecvNS < b.ControllerSendNS || b.OwnerMonoNS < 0 {
			return 0, 0, false
		}
		delta := g - b.OwnerMonoNS
		absDelta := delta
		if absDelta < 0 {
			absDelta = -absDelta
		}
		skew, skewOK := browserClockSkewBoundsNS(absDelta, maxSkewPPM)
		if !skewOK {
			return 0, 0, false
		}
		sendPlusDelta, ok1 := addInt64Checked(b.ControllerSendNS, delta)
		bLow, ok2 := addInt64Checked(sendPlusDelta, -skew)
		recvPlusDelta, ok3 := addInt64Checked(b.ControllerRecvNS, delta)
		bHigh, ok4 := addInt64Checked(recvPlusDelta, skew)
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return 0, 0, false
		}
		if bLow > low {
			low = bLow
		}
		if bHigh < high {
			high = bHigh
		}
	}
	if low > high {
		return low, high, false
	}
	return low, high, true
}

func (r *BrowserSourceReconciler) ValidateDistributedClocks() (bool, string) {
	if !r.Complete() {
		return false, "browser_source_closure_incomplete"
	}
	if r.clockEnvelope == nil {
		return false, "browser_clock_envelope_missing"
	}
	maxSkewPPM, maxRTT, maxGap, ok := r.distributedClockParams()
	if !ok {
		return false, "browser_clock_envelope_invalid"
	}
	env := r.clockEnvelope
	if len(env.Owners) != len(r.owners) {
		return false, "browser_clock_owner_brackets_missing"
	}
	ownerIDs := make([]string, 0, len(r.owners))
	for id := range r.owners {
		ownerIDs = append(ownerIDs, id)
	}
	slices.Sort(ownerIDs)
	for _, id := range ownerIDs {
		owner := r.owners[id]
		brackets := env.Owners[id]
		if len(brackets) < 2 || len(brackets) > MaxBrowserClockBracketsPerOwner {
			return false, "browser_clock_owner_brackets_missing"
		}
		for i, b := range brackets {
			if b.Authority != owner.authority || b.OwnerID != id || b.ControllerSendNS < 0 || b.ControllerRecvNS < b.ControllerSendNS || b.OwnerMonoNS < 0 {
				return false, "browser_clock_bracket_invalid"
			}
			if b.ControllerRecvNS-b.ControllerSendNS > maxRTT {
				return false, "browser_clock_bracket_rtt_exceeded"
			}
			if i > 0 {
				if b.OwnerMonoNS < brackets[i-1].OwnerMonoNS {
					return false, "browser_clock_brackets_inconsistent"
				}
				if b.OwnerMonoNS-brackets[i-1].OwnerMonoNS > maxGap {
					return false, "browser_clock_bracket_gap_exceeded"
				}
			}
		}
		firstActivity := owner.at
		if owner.hasActivity && owner.firstActivityNS < firstActivity {
			firstActivity = owner.firstActivityNS
		}
		if brackets[0].OwnerMonoNS > firstActivity || brackets[len(brackets)-1].OwnerMonoNS < owner.at {
			return false, "browser_clock_brackets_unbounded"
		}
		for _, b := range brackets {
			if _, _, ok := projectDistributedOwnerReading(owner.authority, brackets, maxSkewPPM, b.OwnerMonoNS); !ok {
				return false, "browser_clock_brackets_inconsistent"
			}
		}
	}
	return true, ""
}

func (r *BrowserSourceReconciler) distributedSteadySpans(maxSkewPPM int64) ([]BrowserSteadyProof, bool) {
	env := r.clockEnvelope
	ownerIDs := make([]string, 0, len(r.owners))
	for id := range r.owners {
		ownerIDs = append(ownerIDs, id)
	}
	slices.Sort(ownerIDs)
	var fleetSpans []BrowserSteadyProof
	for idx, id := range ownerIDs {
		owner := r.owners[id]
		brackets := env.Owners[id]
		var ownerSpans []BrowserSteadyProof
		for _, span := range owner.spans {
			_, startHigh, ok1 := projectDistributedOwnerReading(owner.authority, brackets, maxSkewPPM, span.StartNS)
			endLow, _, ok2 := projectDistributedOwnerReading(owner.authority, brackets, maxSkewPPM, span.EndNS)
			if !ok1 || !ok2 {
				return nil, false
			}
			if endLow > startHigh {
				ownerSpans = append(ownerSpans, BrowserSteadyProof{
					BrowserMonotonicSpan: BrowserMonotonicSpan{
						Authority: env.Controller,
						StartNS:   startHigh,
						EndNS:     endLow,
					},
					VUsers: span.VUsers,
				})
			}
		}
		if idx == 0 {
			fleetSpans = ownerSpans
			continue
		}
		var next []BrowserSteadyProof
		for _, a := range fleetSpans {
			for _, b := range ownerSpans {
				start := max(a.StartNS, b.StartNS)
				end := min(a.EndNS, b.EndNS)
				if end > start {
					next = append(next, BrowserSteadyProof{
						BrowserMonotonicSpan: BrowserMonotonicSpan{
							Authority: env.Controller,
							StartNS:   start,
							EndNS:     end,
						},
						VUsers: a.VUsers + b.VUsers,
					})
				}
			}
		}
		fleetSpans = next
	}
	return fleetSpans, true
}

// MapDistributed projects an owner-verified document observation and the
// multi-generator steady-load spans into the controller's monotonic authority
// domain using periodic RPC causal brackets and bounded crystal skew.
func (r *BrowserSourceReconciler) MapDistributed(o BrowserDocumentObservation) (BrowserMonotonicSpan, time.Duration, []BrowserSteadyProof, error) {
	if ok, reason := r.ValidateDistributedClocks(); !ok {
		return BrowserMonotonicSpan{}, 0, nil, fmt.Errorf("browser distributed clock mapping unavailable: %s", reason)
	}
	localWindow, _, err := r.Map(o)
	if err != nil {
		return BrowserMonotonicSpan{}, 0, nil, err
	}
	maxSkewPPM, _, _, ok := r.distributedClockParams()
	if !ok {
		return BrowserMonotonicSpan{}, 0, nil, errors.New("browser distributed clock parameters invalid")
	}
	owner := r.generations[o.SourceGeneration]
	if owner == nil {
		return BrowserMonotonicSpan{}, 0, nil, errors.New("browser distributed clock owner missing")
	}
	brackets := r.clockEnvelope.Owners[owner.identity]
	startLow, startHigh, ok1 := projectDistributedOwnerReading(owner.authority, brackets, maxSkewPPM, localWindow.StartNS)
	endLow, endHigh, ok2 := projectDistributedOwnerReading(owner.authority, brackets, maxSkewPPM, localWindow.EndNS)
	if !ok1 || !ok2 || endHigh < startLow {
		return BrowserMonotonicSpan{}, 0, nil, errors.New("browser distributed clock projection failed")
	}
	spans, ok := r.distributedSteadySpans(maxSkewPPM)
	if !ok {
		return BrowserMonotonicSpan{}, 0, nil, errors.New("browser distributed steady span projection failed")
	}
	uncertainty := time.Duration(max(startHigh-startLow, endHigh-endLow))
	return BrowserMonotonicSpan{
		Authority: r.clockEnvelope.Controller,
		StartNS:   startLow,
		EndNS:     endHigh,
	}, uncertainty, spans, nil
}

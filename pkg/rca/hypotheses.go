package rca

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/rca/signatures"
)

// Hypothesis parameters.
const (
	// LeadMargin is how long before a symptom's onset a cause may begin.
	LeadMargin = 5 * time.Minute
	// MaxGraphHops bounds the walk from a symptom through the run's graph.
	MaxGraphHops = 8
	// MaxHypotheses bounds the ranked list; the strongest are kept.
	MaxHypotheses = 100
)

// Confidence levels.
const (
	ConfidenceHigh   = "high"
	ConfidenceMedium = "medium"
	ConfidenceLow    = "low"
)

// Chain relations between a cause's onset and its symptom's.
const (
	RelationPrecedes       = signatures.Precedes
	RelationUnordered      = signatures.Unordered
	RelationLoadCoincident = "load_coincident"
)

// Hypothesis limitations.
const (
	LimitationValidityCapsConfidence = "validity_caps_confidence"
	LimitationNoSymptoms             = "no_symptoms"
	LimitationHypothesesTruncated    = "hypotheses_truncated"
	LimitationSignaturesUnmeasured   = "signatures_unmeasured"
)

// measures is the evidence kind a series measures.
func measures(s Series) string {
	if s.Measures != "" {
		return s.Measures
	}
	switch s.Kind {
	case SeriesLatency:
		return signatures.KindLatency
	case SeriesErrorRate:
		return signatures.KindErrors
	case SeriesThroughput:
		return signatures.KindThroughput
	}
	return ""
}

// seriesNodes is the graph nodes a series measures; a transaction, action or
// dependency series defaults to its own node.
func seriesNodes(s Series) []string {
	if len(s.Nodes) > 0 {
		return s.Nodes
	}
	parts := strings.SplitN(s.ID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	name := parts[1]
	if i := strings.LastIndex(name, "/"); i > 0 {
		name = name[:i]
	}
	switch parts[0] {
	case NodeTransaction:
		return []string{TransactionNodeID(name)}
	case NodeAction:
		return []string{ActionNodeID(name)}
	case NodeDependency:
		return []string{DependencyNodeID(name)}
	}
	return nil
}

// symptomKind is the symptom an anomaly on a transaction or action series
// is: rising latency or errors, or falling throughput; "" otherwise.
func symptomKind(a Anomaly, s Series) string {
	if !strings.HasPrefix(s.ID, NodeTransaction+"/") && !strings.HasPrefix(s.ID, NodeAction+"/") {
		return ""
	}
	switch kind := measures(s); {
	case kind == signatures.KindLatency && a.Direction == DirectionIncrease,
		kind == signatures.KindErrors && a.Direction == DirectionIncrease,
		kind == signatures.KindThroughput && a.Direction == DirectionDecrease:
		return kind
	}
	return ""
}

// graphWalker walks the graph's used edges: calls and maps_to forward;
// contains forward, and back between provider nodes so an entity reaches its
// instances and parents; shares_infrastructure both ways so co-located
// entities meet.
type graphWalker struct {
	graph Graph
	next  map[string][]string
}

func newGraphWalker(g Graph) graphWalker {
	kinds := make(map[string]string, len(g.Nodes))
	for _, n := range g.Nodes {
		kinds[n.ID] = n.Kind
	}
	provider := func(id string) bool {
		return kinds[id] == NodeProviderEntity || kinds[id] == NodeInfrastructure
	}
	next := map[string][]string{}
	for _, e := range g.Edges {
		if !e.Used {
			continue
		}
		switch e.Kind {
		case EdgeCalls, EdgeMapsTo:
			next[e.From] = append(next[e.From], e.To)
		case EdgeContains:
			next[e.From] = append(next[e.From], e.To)
			if provider(e.From) && provider(e.To) {
				next[e.To] = append(next[e.To], e.From)
			}
		case EdgeSharesInfrastructure:
			next[e.From] = append(next[e.From], e.To)
			next[e.To] = append(next[e.To], e.From)
		}
	}
	return graphWalker{graph: g, next: next}
}

// reach returns the nodes within MaxGraphHops of the start nodes.
func (w graphWalker) reach(start []string) map[string]bool {
	reached := map[string]bool{}
	frontier := []string{}
	for _, id := range start {
		if id = w.graph.Resolve(id); !reached[id] {
			reached[id] = true
			frontier = append(frontier, id)
		}
	}
	for hop := 0; hop < MaxGraphHops && len(frontier) > 0; hop++ {
		var following []string
		for _, id := range frontier {
			for _, to := range w.next[id] {
				if !reached[to] {
					reached[to] = true
					following = append(following, to)
				}
			}
		}
		frontier = following
	}
	return reached
}

// connected reports whether any of a series' nodes, through the graph's
// aliases, was reached.
func (w graphWalker) connected(nodes []string, reached map[string]bool) bool {
	for _, n := range nodes {
		if reached[w.graph.Resolve(n)] {
			return true
		}
	}
	return false
}

// onsetOrder orders an observation's onset against a symptom's: a precedes
// b only when b began later by more than both uncertainties.
func onsetOrder(cause, symptom Anomaly) string {
	gap := symptom.Onset.Sub(cause.Onset)
	tolerance := cause.OnsetUncertainty + symptom.OnsetUncertainty
	switch {
	case gap > tolerance:
		return signatures.Precedes
	case -gap > tolerance:
		return signatures.Follows
	}
	return signatures.Unordered
}

// loadCoincident reports whether an onset fell at a load step: within its
// uncertainty of the settle period that starts a level.
func loadCoincident(a Anomaly, levels []LoadLevel, settle time.Duration) bool {
	for _, l := range levels {
		step := l.Start.Add(-settle)
		if !a.Onset.Before(step.Add(-a.OnsetUncertainty)) && !a.Onset.After(l.Start.Add(a.OnsetUncertainty)) {
			return true
		}
	}
	return false
}

// Hypothesize judges every signature for every symptom and returns the
// supported hypotheses, ranked, with the assessment's outcome: supported
// when any hypothesis is; insufficient_evidence when no signature's
// evidence was measured; no_supported_hypothesis otherwise, including when
// there is no symptom to explain. Only series detection judged in a
// symptom's scope count as measured, and one cause behind several symptoms
// is one hypothesis.
func Hypothesize(anomalies []Anomaly, series []Series, judged JudgedSeries, graph Graph, levels []LoadLevel, params DetectParams) ([]Hypothesis, string, []Limitation) {
	byID := make(map[string]Series, len(series))
	for _, s := range series {
		byID[s.ID] = s
	}
	walker := newGraphWalker(graph)
	all := signatures.All()
	merged := map[string]*Hypothesis{}
	var limitations []Limitation
	symptoms, measured := 0, 0
	for _, symptom := range anomalies {
		kind := symptomKind(symptom, byID[symptom.Series])
		if kind == "" {
			continue
		}
		symptoms++
		reached := walker.reach(seriesNodes(byID[symptom.Series]))
		ctx := signatures.Context{
			Symptom:   signatures.Observation{AnomalyID: symptom.ID, Series: symptom.Series, Kind: kind, Direction: symptom.Direction, PeakZ: symptom.PeakZ, LoadCoincident: loadCoincident(symptom, levels, params.LevelSettle)},
			Available: map[string]signatures.Availability{},
		}
		for _, s := range series {
			evidence := measures(s)
			if evidence == "" || !s.External() && !strings.HasPrefix(s.ID, NodeDependency+"/") || !judged.Judged(symptom.Scope, s.ID) {
				continue
			}
			available := ctx.Available[evidence]
			available.Any = true
			available.Connected = available.Connected || walker.connected(seriesNodes(s), reached)
			ctx.Available[evidence] = available
		}
		causes := map[string]Anomaly{}
		causeNodes := map[string][]string{}
		windowStart := symptom.Onset.Add(-LeadMargin)
		for _, a := range anomalies {
			s := byID[a.Series]
			evidence := measures(s)
			if a.ID == symptom.ID || a.Scope != symptom.Scope || evidence == "" || symptomKind(a, s) != "" ||
				evidence == signatures.KindLatency || evidence == signatures.KindErrors || evidence == signatures.KindThroughput {
				continue
			}
			if a.Onset.Before(windowStart) || a.Onset.After(symptom.End) {
				continue
			}
			causes[a.ID] = a
			for _, n := range seriesNodes(s) {
				causeNodes[a.ID] = append(causeNodes[a.ID], graph.Resolve(n))
			}
			ctx.Observations = append(ctx.Observations, signatures.Observation{
				AnomalyID: a.ID, Series: a.Series, Kind: evidence, Direction: a.Direction, PeakZ: a.PeakZ,
				Order: onsetOrder(a, symptom), LoadCoincident: loadCoincident(a, levels, params.LevelSettle), Connected: walker.connected(seriesNodes(s), reached),
			})
		}
		results := make(map[string]signatures.Result, len(all))
		var supported []string
		for _, sig := range all {
			r := sig.Judge(ctx)
			results[sig.Name] = r
			if r.Verdict != signatures.MissingEvidence && r.Verdict != signatures.NotApplicable {
				measured++
			}
			if r.Verdict == signatures.Supported {
				supported = append(supported, sig.Name)
			}
		}
		for _, sig := range all {
			if r := results[sig.Name]; r.Verdict == signatures.Supported {
				mergeHypothesis(merged, buildHypothesis(sig, r, symptom, causes, causeNodes, results, supported), r)
			}
		}
	}
	hypotheses := make([]Hypothesis, 0, len(merged))
	for _, h := range merged {
		finishHypothesis(h)
		hypotheses = append(hypotheses, *h)
	}
	sort.SliceStable(hypotheses, func(i, j int) bool {
		if hypotheses[i].Score != hypotheses[j].Score {
			return hypotheses[i].Score > hypotheses[j].Score
		}
		return hypotheses[i].ID < hypotheses[j].ID
	})
	if len(hypotheses) > MaxHypotheses {
		limitations = append(limitations, Limitation{Code: LimitationHypothesesTruncated, Detail: fmt.Sprintf("kept the %d strongest of %d hypotheses", MaxHypotheses, len(hypotheses))})
		hypotheses = hypotheses[:MaxHypotheses]
	}
	for i := range hypotheses {
		hypotheses[i].Rank = i + 1
	}
	switch {
	case symptoms == 0:
		limitations = append(limitations, Limitation{Code: LimitationNoSymptoms, Detail: "no transaction latency, error or throughput anomaly to explain"})
		return hypotheses, OutcomeNoSupportedHypothesis, limitations
	case len(hypotheses) > 0:
		return hypotheses, OutcomeSupported, limitations
	case measured == 0:
		limitations = append(limitations, Limitation{Code: LimitationSignaturesUnmeasured, Detail: fmt.Sprintf("none of the %d signatures had its required evidence measured", len(all))})
		return hypotheses, OutcomeInsufficientEvidence, limitations
	}
	return hypotheses, OutcomeNoSupportedHypothesis, limitations
}

// buildHypothesis turns a supported verdict for one symptom into a
// hypothesis: its chain of onsets, the evidence for and against it, the
// hypotheses competing with it and how confident the evidence allows it to
// be.
func buildHypothesis(sig signatures.Signature, r signatures.Result, symptom Anomaly, causes map[string]Anomaly, causeNodes map[string][]string, results map[string]signatures.Result, supported []string) Hypothesis {
	h := Hypothesis{
		Signature: sig.Name, Symptoms: []string{symptom.ID},
		Score: math.Round(r.Score*1000) / 1000, Falsification: sig.Falsification,
		Chain: []ChainStep{}, Supporting: []EvidenceRef{}, Contradicting: []EvidenceRef{}, Competing: []string{}, Limitations: []string{}, ConfidenceReasons: []string{}, Nodes: []string{},
	}
	connected, precedes := false, false
	for _, o := range r.Supporting {
		h.Nodes = append(h.Nodes, causeNodes[o.AnomalyID]...)
		relation := o.Order
		if o.LoadCoincident {
			relation = RelationLoadCoincident
		}
		h.Chain = append(h.Chain, ChainStep{From: o.AnomalyID, To: symptom.ID, Relation: relation})
		h.Supporting = append(h.Supporting, causes[o.AnomalyID].Evidence...)
		connected = connected || o.Connected
		precedes = precedes || o.Order == signatures.Precedes
	}
	for _, o := range r.Contradicting {
		h.Contradicting = append(h.Contradicting, causes[o.AnomalyID].Evidence...)
	}
	for _, other := range supported {
		if other != sig.Name {
			h.Competing = append(h.Competing, other)
		}
	}
	for _, name := range sig.Competing {
		if results[name].Verdict == signatures.MissingEvidence {
			h.Limitations = append(h.Limitations, fmt.Sprintf("competing %s could not be judged: %s not measured", name, strings.Join(results[name].Missing, ", ")))
		}
	}
	clean := len(r.Contradicting) == 0 && len(r.Reasons) == 0
	switch {
	case connected && precedes && clean:
		h.Confidence = ConfidenceHigh
	case connected:
		h.Confidence = ConfidenceMedium
	default:
		h.Confidence = ConfidenceLow
	}
	if connected {
		h.ConfidenceReasons = append(h.ConfidenceReasons, "the evidence is connected to the symptom through the run's graph")
	} else {
		h.ConfidenceReasons = append(h.ConfidenceReasons, "no used graph path connects the evidence to the symptom; approve the dependency's mapping to connect it")
	}
	if precedes {
		h.ConfidenceReasons = append(h.ConfidenceReasons, "the evidence began before the symptom by more than both onsets' uncertainty")
	} else {
		h.ConfidenceReasons = append(h.ConfidenceReasons, "the evidence and the symptom began within their onsets' uncertainty, so their order is unknown")
	}
	h.ConfidenceReasons = append(append(h.ConfidenceReasons, r.Notes...), r.Reasons...)
	return h
}

func confidenceOrder(c string) int {
	switch c {
	case ConfidenceHigh:
		return 0
	case ConfidenceMedium:
		return 1
	}
	return 2
}

// mergeHypothesis folds a symptom's hypothesis into the one with the same
// signature and supporting series: one cause behind several symptoms, or
// seen both over time and against the baseline, is one hypothesis, as
// strong as its strongest instance.
func mergeHypothesis(merged map[string]*Hypothesis, h Hypothesis, r signatures.Result) {
	support := make([]string, 0, len(r.Supporting))
	for _, o := range r.Supporting {
		support = append(support, o.Series)
	}
	support = uniqueSorted(support)
	key := h.Signature + "\x00" + strings.Join(support, "\x00")
	existing := merged[key]
	if existing == nil {
		h.ID = stableID("hypothesis", h.Signature, strings.Join(support, ","))
		merged[key] = &h
		return
	}
	existing.Symptoms = append(existing.Symptoms, h.Symptoms...)
	existing.Chain = append(existing.Chain, h.Chain...)
	existing.Supporting = append(existing.Supporting, h.Supporting...)
	existing.Contradicting = append(existing.Contradicting, h.Contradicting...)
	existing.Competing = append(existing.Competing, h.Competing...)
	existing.Limitations = append(existing.Limitations, h.Limitations...)
	existing.Nodes = append(existing.Nodes, h.Nodes...)
	if h.Score > existing.Score {
		existing.Score = h.Score
	}
	if confidenceOrder(h.Confidence) < confidenceOrder(existing.Confidence) {
		existing.Confidence, existing.ConfidenceReasons = h.Confidence, h.ConfidenceReasons
	}
}

// finishHypothesis orders and de-duplicates a merged hypothesis's lists.
func finishHypothesis(h *Hypothesis) {
	h.Symptoms = uniqueSorted(h.Symptoms)
	h.Competing = uniqueSorted(h.Competing)
	h.Limitations = uniqueSorted(h.Limitations)
	h.Nodes = uniqueSorted(h.Nodes)
	sort.SliceStable(h.Chain, func(i, j int) bool {
		if h.Chain[i].To != h.Chain[j].To {
			return h.Chain[i].To < h.Chain[j].To
		}
		return h.Chain[i].From < h.Chain[j].From
	})
	h.Chain = uniqueBy(h.Chain, func(c ChainStep) string { return c.From + "\x00" + c.To })
	h.Supporting = uniqueBy(h.Supporting, func(e EvidenceRef) string { return e.ID })
	h.Contradicting = uniqueBy(h.Contradicting, func(e EvidenceRef) string { return e.ID })
	if len(h.Symptoms) > 1 {
		h.ConfidenceReasons = append(h.ConfidenceReasons, fmt.Sprintf("the same evidence explains %d symptoms", len(h.Symptoms)))
	}
	if len(h.Competing) > 0 {
		h.ConfidenceReasons = append(h.ConfidenceReasons, fmt.Sprintf("%d other hypotheses are also supported for these symptoms", len(h.Competing)))
	}
}

// capConfidenceByValidity lowers what a run's validity verdict allows: an
// invalid run's load did not reach the system as reported, so every
// hypothesis is low; a suspect or unknown run allows at most medium. It
// reports the limitation when it capped anything.
func capConfidenceByValidity(hypotheses []Hypothesis, v Validity) (Limitation, bool) {
	ceiling, reason := ConfidenceHigh, ""
	switch v.Verdict {
	case ValidityInvalid:
		ceiling, reason = ConfidenceLow, "the run is invalid ("+strings.Join(v.Reasons, ", ")+"), so its symptoms may come from the load generators rather than the system"
	case ValiditySuspect, ValidityUnknown:
		ceiling, reason = ConfidenceMedium, "the run's validity is "+v.Verdict+", so its symptoms may partly come from the load generators"
	}
	capped := false
	for i := range hypotheses {
		if confidenceOrder(hypotheses[i].Confidence) < confidenceOrder(ceiling) {
			hypotheses[i].Confidence = ceiling
			hypotheses[i].ConfidenceReasons = append(hypotheses[i].ConfidenceReasons, reason)
			capped = true
		}
	}
	return Limitation{Code: LimitationValidityCapsConfidence, Detail: reason}, capped
}

// uniqueBy keeps the first of each key, in order.
func uniqueBy[T any](items []T, key func(T) string) []T {
	seen := map[string]bool{}
	out := items[:0:0]
	for _, item := range items {
		if k := key(item); !seen[k] {
			seen[k] = true
			out = append(out, item)
		}
	}
	return out
}

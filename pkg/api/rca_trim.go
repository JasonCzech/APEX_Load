package api

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// limitationAssessmentTrimmed: the stored assessment was cut to fit the
// stored payload limit.
const limitationAssessmentTrimmed = "assessment_trimmed"

// rcaTrimHeadroom leaves room for the trimming limitation itself.
const rcaTrimHeadroom = 1 << 10

// trimRCAAssessment cuts an assessment until fits reports true, the least
// load-bearing parts first: validity windows, spikes, capacity results,
// browser baseline results, the graph's unused edges and then its weakest edges and uncited nodes,
// anomalies no hypothesis cites, and then the lowest-ranked hypotheses,
// whose anomalies go with them. A kept hypothesis's anomalies and nodes are
// never cut. It reports whether it cut anything.
func trimRCAAssessment(a *rca.Assessment, fits func() bool) bool {
	cut := false
	halve := func(n int, apply func(int)) {
		for !fits() && n > 0 {
			n /= 2
			apply(n)
			cut = true
		}
	}
	halve(len(a.Validity.Windows), func(n int) { a.Validity.Windows = a.Validity.Windows[:n] })
	halve(len(a.Detection.Spikes), func(n int) { a.Detection.Spikes = a.Detection.Spikes[:n] })
	halve(max(len(a.Capacity), len(a.LittlesLaw)), func(n int) {
		a.Capacity, a.LittlesLaw = a.Capacity[:min(n, len(a.Capacity))], a.LittlesLaw[:min(n, len(a.LittlesLaw))]
	})
	// Browser baseline results are advisory and never cited, so they go
	// before the graph, anomalies and hypotheses (anomalies first are kept).
	// The section is copied first: a bundle shares the stored assessment's.
	if baseline := a.BrowserBaseline; baseline != nil && len(baseline.Results) > 0 && !fits() {
		trimmed := *baseline
		trimmed.Results, trimmed.Limitations = slices.Clone(baseline.Results), slices.Clone(baseline.Limitations)
		a.BrowserBaseline = &trimmed
		before := len(trimmed.Results)
		halve(before, func(n int) { trimmed.Results = trimmed.Results[:n] })
		trimmed.Status = rca.DetectionPartial
		trimmed.Limitations = append(trimmed.Limitations, rca.Limitation{Code: rca.BrowserBaselineResultsTruncated, Detail: fmt.Sprintf("kept %d of %d browser baseline results to fit the assessment's size limit", len(trimmed.Results), before)})
	}
	if !fits() && len(a.Graph.Edges) > 0 {
		used := a.Graph.Edges[:0:0]
		for _, e := range a.Graph.Edges {
			if e.Used {
				used = append(used, e)
			}
		}
		a.Graph.Edges, cut = used, true
		// Edges are stored strongest first.
		halve(len(a.Graph.Edges), func(n int) { a.Graph.Edges = a.Graph.Edges[:n] })
	}
	if !fits() && len(a.Graph.Nodes) > 0 {
		cited, other := splitCited(a.Graph.Nodes, citedNodes(a), func(n rca.GraphNode) string { return n.ID })
		halve(len(other), func(n int) { a.Graph.Nodes = append(slices.Clone(cited), other[:n]...) })
	}
	trimUncitedAnomalies(a, fits, &cut)
	// Hypotheses are stored best first; each cut frees the anomalies only
	// the dropped ones cited.
	for !fits() && len(a.Hypotheses) > 0 {
		a.Hypotheses, cut = a.Hypotheses[:len(a.Hypotheses)/2], true
		trimUncitedAnomalies(a, fits, &cut)
	}
	return cut
}

// trimRCADetails cuts the SLA details, which come from the manifest
// unbounded; it is the last resort.
func trimRCADetails(a *rca.Assessment, fits func() bool) bool {
	cut := false
	for !fits() && len(a.Acceptance.Details) > 0 {
		a.Acceptance.Details, cut = a.Acceptance.Details[:len(a.Acceptance.Details)/2], true
	}
	return cut
}

// trimUncitedAnomalies drops the weakest anomalies no hypothesis cites,
// keeping the rest in onset order.
func trimUncitedAnomalies(a *rca.Assessment, fits func() bool, cut *bool) {
	if fits() {
		return
	}
	cited, other := splitCited(a.Anomalies, citedAnomalies(a), func(x rca.Anomaly) string { return x.ID })
	slices.SortStableFunc(other, func(x, y rca.Anomaly) int { return cmp.Compare(math.Abs(y.PeakZ), math.Abs(x.PeakZ)) })
	for !fits() && len(other) > 0 {
		other = other[:len(other)/2]
		kept := append(slices.Clone(cited), other...)
		slices.SortStableFunc(kept, func(x, y rca.Anomaly) int {
			if c := x.Onset.Compare(y.Onset); c != 0 {
				return c
			}
			return cmp.Compare(x.ID, y.ID)
		})
		a.Anomalies, *cut = kept, true
	}
}

func citedAnomalies(a *rca.Assessment) map[string]bool {
	cited := map[string]bool{}
	for _, h := range a.Hypotheses {
		for _, step := range h.Chain {
			cited[step.From], cited[step.To] = true, true
		}
		for _, s := range h.Symptoms {
			cited[s] = true
		}
	}
	return cited
}

func citedNodes(a *rca.Assessment) map[string]bool {
	cited := map[string]bool{}
	for _, h := range a.Hypotheses {
		for _, n := range h.Nodes {
			cited[n] = true
		}
	}
	return cited
}

// splitCited separates the items whose key is cited from the rest, keeping
// order.
func splitCited[T any](items []T, cited map[string]bool, key func(T) string) ([]T, []T) {
	var in, out []T
	for _, item := range items {
		if cited[key(item)] {
			in = append(in, item)
		} else {
			out = append(out, item)
		}
	}
	return in, out
}

// boundStoredRCAPayload encodes an analysis payload within the stored limit,
// trimming its assessment rather than failing it.
func boundStoredRCAPayload(payload *rcaAnalysisPayload) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil || len(body) <= storage.MaxRCAPayloadBytes || payload.Assessment == nil {
		return body, err
	}
	fits := func() bool {
		b, err := json.Marshal(payload)
		return err == nil && len(b) <= storage.MaxRCAPayloadBytes-rcaTrimHeadroom
	}
	offered := len(body)
	// Excerpts no hypothesis cites go first; the excerpts of anomalies the
	// cut assessment drops go with them, and the rest go before the SLA
	// details.
	cutExcerpts := dropUncitedRCAExcerpts(payload.Assessment, &payload.Excerpts, fits)
	cutAssessment := trimRCAAssessment(payload.Assessment, fits)
	payload.Excerpts = citedExcerpts(payload.Assessment, payload.Excerpts)
	cutExcerpts = halveRCAExcerpts(&payload.Excerpts, fits) || cutExcerpts
	if cutExcerpts || cutAssessment || trimRCADetails(payload.Assessment, fits) {
		payload.Assessment.Limitations = append(payload.Assessment.Limitations, rca.Limitation{Code: limitationAssessmentTrimmed,
			Detail: fmt.Sprintf("the assessment and its evidence excerpts were %d bytes and were cut to the stored limit of %d; the least load-bearing parts went first", offered, storage.MaxRCAPayloadBytes)})
	}
	if body, err = json.Marshal(payload); err == nil && len(body) > storage.MaxRCAPayloadBytes {
		err = errors.New("assessment exceeds the stored size limit")
	}
	return body, err
}

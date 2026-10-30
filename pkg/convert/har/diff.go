package har

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/protocol/jsonextract"
)

const (
	DiffClassificationDynamic   = "dynamic"
	DiffClassificationStatic    = "static"
	DiffClassificationCandidate = "candidate"
)

// DiffMatch* say how a baseline correlation found its comparison value:
// re-extracting its extractor from the comparison request aligned with the
// baseline producer, or (when the extractor cannot be re-evaluated offline)
// pairing it with the comparison correlation of the same name.
const (
	DiffMatchAlignedRequest  = "aligned_request"
	DiffMatchCorrelationName = "correlation_name"
)

type DiffReport struct {
	ArtifactMode string `json:"artifact_mode"`
	Compared     bool   `json:"compared"`
	// Reason says why no comparison was performed when Compared is false.
	Reason                 string               `json:"reason,omitempty"`
	BaselineCorrelations   int                  `json:"baseline_correlations"`
	ComparisonCorrelations int                  `json:"comparison_correlations"`
	DynamicCandidates      int                  `json:"dynamic_candidates"`
	StaticCandidates       int                  `json:"static_candidates"`
	UnmatchedCandidates    int                  `json:"unmatched_candidates"`
	Classifications        []DiffClassification `json:"classifications,omitempty"`
	Warnings               []string             `json:"warnings,omitempty"`
}

type DiffClassification struct {
	Name            string  `json:"name"`
	Source          string  `json:"source,omitempty"`
	Expression      string  `json:"expression,omitempty"`
	ExtractType     string  `json:"extract_type,omitempty"`
	SampleValue     string  `json:"sample_value,omitempty"`
	BaselineValue   string  `json:"baseline_value,omitempty"`
	ComparisonValue string  `json:"comparison_value,omitempty"`
	FoundInStep     int     `json:"found_in_step,omitempty"`
	UsedInSteps     []int   `json:"used_in_steps,omitempty"`
	Classification  string  `json:"classification"`
	DiffConfidence  float64 `json:"diff_confidence"`
	Reason          string  `json:"reason"`
	// BaselineEntry and ComparisonEntry are the 1-based HAR entries of the
	// producing response in each recording, when they were aligned; Match says
	// how the comparison value was found (DiffMatch*).
	BaselineEntry   int    `json:"baseline_entry,omitempty"`
	ComparisonEntry int    `json:"comparison_entry,omitempty"`
	Match           string `json:"match,omitempty"`
}

// ComparisonRecording is a comparison HAR converted and indexed for
// classification against a baseline. It holds source values and must stay
// private; only the DiffReport built from it is public.
type ComparisonRecording struct {
	correlations []sample.Correlation
	observations []sample.HARSourceObservation
}

// PrepareComparison converts a comparison HAR once so it can be classified
// against an already-converted baseline (DiffPrepared). It depends only on its
// arguments, so a caller may run it concurrently with the baseline conversion;
// opts is read, never written (Profile and the other shared pointers included).
func PrepareComparison(comparison []byte, opts ConvertOptions) (*ComparisonRecording, error) {
	return PrepareComparisonContext(context.Background(), comparison, opts)
}

// PrepareComparisonContext is PrepareComparison that stops with ctx. A
// conversion cannot be interrupted part-way, so ctx is checked before the
// conversion and before indexing its result.
func PrepareComparisonContext(ctx context.Context, comparison []byte, opts ConvertOptions) (*ComparisonRecording, error) {
	if len(bytes.TrimSpace(comparison)) == 0 {
		return nil, fmt.Errorf("comparison HAR content is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opts.ArtifactMode = ArtifactModeDesignHARPair
	result, err := Convert(comparison, opts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prepared := &ComparisonRecording{correlations: result.Correlations}
	if result.Document != nil {
		// Without observations every correlation falls back to name pairing.
		prepared.observations, _ = result.Document.SourceObservations(true)
	}
	return prepared, nil
}

// Diff compares two recordings of the same flow and classifies correlation
// candidates as dynamic when the reused value changes between runs, static
// when it is stable, or candidate when it was only observed in the baseline.
func Diff(baseline []byte, comparison []byte, opts ConvertOptions) (*DiffReport, error) {
	if len(bytes.TrimSpace(comparison)) == 0 {
		return nil, fmt.Errorf("comparison HAR content is required")
	}
	opts.ArtifactMode = ArtifactModeDesignHARPair
	baselineResult, err := Convert(baseline, opts)
	if err != nil {
		return nil, fmt.Errorf("baseline HAR: %w", err)
	}
	return DiffWithBaseline(baselineResult, comparison, opts)
}

// DiffWithBaseline is Diff for a baseline the caller already converted with
// the same options. The artifact mode only shapes governance, and correlations
// are final once sample conversion returns, so the baseline is not converted
// a second time.
func DiffWithBaseline(baseline *ConvertResult, comparison []byte, opts ConvertOptions) (*DiffReport, error) {
	prepared, err := PrepareComparison(comparison, opts)
	if err != nil {
		return nil, fmt.Errorf("comparison HAR: %w", err)
	}
	return DiffPrepared(baseline, prepared)
}

// DiffPrepared classifies each baseline correlation against a prepared
// comparison recording. The baseline producer's request is aligned with a
// comparison request (method, host and path; then the query-key set and the
// occurrence order break ties), and the same extractor is evaluated on that
// response. A correlation whose extractor cannot be re-evaluated offline is
// paired with the comparison correlation of the same name instead.
func DiffPrepared(baseline *ConvertResult, comparison *ComparisonRecording) (*DiffReport, error) {
	return DiffPreparedContext(context.Background(), baseline, comparison)
}

// DiffPreparedContext is DiffPrepared that stops with ctx, checked before each
// baseline correlation is classified.
func DiffPreparedContext(ctx context.Context, baseline *ConvertResult, comparison *ComparisonRecording) (*DiffReport, error) {
	report, _, err := diffPrepared(ctx, baseline, comparison)
	return report, err
}

// diffPrepared is DiffPreparedContext that also returns its aligner, whose
// work counters bound the cost in tests.
func diffPrepared(ctx context.Context, baseline *ConvertResult, comparison *ComparisonRecording) (*DiffReport, *diffAligner, error) {
	if baseline == nil {
		return nil, nil, fmt.Errorf("baseline HAR: conversion result is required")
	}
	if comparison == nil {
		return nil, nil, fmt.Errorf("comparison HAR: prepared recording is required")
	}
	comparisonByKey := make(map[string]sample.Correlation, len(comparison.correlations))
	for _, corr := range comparison.correlations {
		comparisonByKey[correlationDiffKey(corr)] = corr
	}
	var baselineObservations []sample.HARSourceObservation
	if baseline.Document != nil && len(baseline.Correlations) > 0 {
		baselineObservations, _ = baseline.Document.SourceObservations(true)
	}
	aligner := newDiffAligner(baselineObservations, comparison.observations, baseline.Correlations, comparison.correlations)

	report := &DiffReport{
		ArtifactMode:           ArtifactModeDesignHARPair,
		Compared:               true,
		BaselineCorrelations:   len(baseline.Correlations),
		ComparisonCorrelations: len(comparison.correlations),
	}
	for _, corr := range baseline.Correlations {
		if err := ctx.Err(); err != nil {
			return nil, aligner, err
		}
		classification := DiffClassification{
			Name:        corr.Name,
			Source:      corr.Source,
			Expression:  corr.Expression,
			ExtractType: corr.ExtractType,
			SampleValue: publicCorrelationValue(corr),
			// Import review is an authoring surface, so retain the exact samples.
			// Runtime logs and debug output apply their own redaction boundary.
			BaselineValue:  publicCorrelationValue(corr),
			FoundInStep:    corr.FoundInStep,
			UsedInSteps:    append([]int(nil), corr.UsedInSteps...),
			Classification: DiffClassificationCandidate,
			DiffConfidence: 0.55,
			Reason:         "value was reused later in the baseline HAR but was not matched in the comparison HAR",
		}
		baselineValue := correlationValue(corr)
		namePaired, hasNamePaired := comparisonByKey[correlationDiffKey(corr)]
		var paired *sample.Correlation
		if hasNamePaired {
			paired = &namePaired
		}
		if outcome, ok := aligner.reextract(corr, baselineValue, paired); ok {
			classification.Match = outcome.match
			classification.BaselineEntry = corr.FoundInEntry
			classification.ComparisonEntry = outcome.entry
			switch {
			case outcome.reason != "":
				classification.Reason = outcome.reason
			case outcome.match == DiffMatchCorrelationName:
				classification.ComparisonValue = publicCorrelationValue(namePaired)
				classifyDiffValues(report, &classification, baselineValue, outcome.value)
			default:
				classification.ComparisonValue = correlate.Truncate(outcome.value, 80)
				classifyDiffValues(report, &classification, baselineValue, outcome.value)
			}
		} else if hasNamePaired {
			classification.Match = DiffMatchCorrelationName
			classification.ComparisonValue = publicCorrelationValue(namePaired)
			classifyDiffValues(report, &classification, baselineValue, correlationValue(namePaired))
		}
		if classification.Classification == DiffClassificationCandidate {
			report.UnmatchedCandidates++
		}
		report.Classifications = append(report.Classifications, classification)
	}

	sort.SliceStable(report.Classifications, func(i, j int) bool {
		left := report.Classifications[i]
		right := report.Classifications[j]
		if diffRank(left.Classification) != diffRank(right.Classification) {
			return diffRank(left.Classification) < diffRank(right.Classification)
		}
		if left.DiffConfidence != right.DiffConfidence {
			return left.DiffConfidence > right.DiffConfidence
		}
		if left.FoundInStep != right.FoundInStep {
			return left.FoundInStep < right.FoundInStep
		}
		return left.Name < right.Name
	})
	return report, aligner, nil
}

func classifyDiffValues(report *DiffReport, classification *DiffClassification, baselineValue, comparisonValue string) {
	if baselineValue != "" && comparisonValue != "" && baselineValue != comparisonValue {
		classification.Classification = DiffClassificationDynamic
		classification.DiffConfidence = 0.95
		classification.Reason = "value changed between recordings and was reused later in both HARs"
		report.DynamicCandidates++
		return
	}
	classification.Classification = DiffClassificationStatic
	classification.DiffConfidence = 0.4
	classification.Reason = "value was reused later but stayed constant across both HARs"
	report.StaticCandidates++
}

// diffAligner pairs baseline producer entries with comparison entries. Entries
// are grouped by method, host and path; within a group the query-key set and
// then the occurrence order pick the counterpart.
type diffAligner struct {
	baseline   map[int]sample.HARSourceObservation
	baselineBy map[string][]sample.HARSourceObservation
	comparison map[string][]sample.HARSourceObservation
	// requests are the comparison observations in recording order, and
	// reusedValues the values comparison detection saw reused: the evidence
	// that a re-extracted value is reused in the comparison too. reuse indexes
	// requests on first use.
	requests     []sample.HARSourceObservation
	reusedValues map[string]bool
	reuse        *diffReuseIndex
	// claimed maps a comparison entry to the baseline producer aligned with
	// it, so a retry prefers responses no other producer was aligned with.
	claimed map[int]int
	// extractions caches extractor results on comparison responses: several
	// correlations often share an extractor and try the same responses.
	extractions map[diffExtractionKey]diffExtraction
	// narrowed caches request groups narrowed to a query-key set.
	narrowed map[diffNarrowKey][]sample.HARSourceObservation
	// baselineValues and comparisonValues are each recording's correlated
	// values, which mask path segments in templates (built on first use).
	baselineValues   []string
	comparisonValues []string
	templates        *diffTemplateGroups
	// evaluations counts extractor runs on comparison responses.
	evaluations int
}

type diffReextraction struct {
	entry  int
	value  string
	reason string
	match  string
}

type diffExtractionKey struct {
	source, extractType, expression string
	entry                           int
}

type diffExtraction struct {
	value string
	err   error
}

type diffNarrowKey struct {
	comparison, template bool
	group, queryKeys     string
}

// diffReuseScanMinLength keeps a value too short to be told apart from
// unrelated request text from counting as reused by a text match alone.
const diffReuseScanMinLength = 4

// diffAlignmentWindow caps how many responses of a request group a producer
// tries on each side of its own position. A long run of same-endpoint
// requests (Salesforce /s/sfsites/aura) then costs each correlation a bounded
// number of extractions, not one per request in the group.
const diffAlignmentWindow = 8

func newDiffAligner(baseline, comparison []sample.HARSourceObservation, baselineCorrelations, comparisonCorrelations []sample.Correlation) *diffAligner {
	aligner := &diffAligner{
		baseline:         make(map[int]sample.HARSourceObservation, len(baseline)),
		baselineBy:       map[string][]sample.HARSourceObservation{},
		comparison:       map[string][]sample.HARSourceObservation{},
		requests:         comparison,
		reusedValues:     make(map[string]bool, 2*len(comparisonCorrelations)),
		claimed:          map[int]int{},
		extractions:      map[diffExtractionKey]diffExtraction{},
		narrowed:         map[diffNarrowKey][]sample.HARSourceObservation{},
		baselineValues:   diffCorrelatedValues(baselineCorrelations),
		comparisonValues: diffCorrelatedValues(comparisonCorrelations),
	}
	for _, observation := range baseline {
		aligner.baseline[observation.ID] = observation
		key := diffRequestKey(observation)
		aligner.baselineBy[key] = append(aligner.baselineBy[key], observation)
	}
	for _, observation := range comparison {
		key := diffRequestKey(observation)
		aligner.comparison[key] = append(aligner.comparison[key], observation)
	}
	for _, value := range aligner.comparisonValues {
		aligner.reusedValues[value] = true
	}
	return aligner
}

// diffCorrelatedValues lists the non-blank values and samples of correlations.
func diffCorrelatedValues(correlations []sample.Correlation) []string {
	var values []string
	for _, corr := range correlations {
		for _, value := range []string{corr.Value, corr.SampleValue} {
			if strings.TrimSpace(value) != "" {
				values = append(values, value)
			}
		}
	}
	return values
}

// reextract evaluates corr's extractor on the comparison response aligned with
// its baseline producer. ok is false when this path cannot decide — the
// extractor is not evaluable offline, or evaluating it on the baseline producer
// does not reproduce the recorded value — and the caller falls back to name
// pairing. A decided outcome carries either the comparison value or the reason
// the correlation stays unmatched.
//
// The response at the producer's position is tried first. When it does not
// yield a value the comparison reuses (a failed attempt, a retry or a
// prefetch recorded before the real producer), the nearby responses of the
// same request group are tried (diffAlignmentWindow), then the comparison
// correlation of the same name (namePaired) when it was produced inside that
// group.
func (a *diffAligner) reextract(corr sample.Correlation, baselineValue string, namePaired *sample.Correlation) (diffReextraction, bool) {
	if a == nil || corr.FoundInEntry <= 0 || baselineValue == "" {
		return diffReextraction{}, false
	}
	producer, ok := a.baseline[corr.FoundInEntry]
	if !ok {
		return diffReextraction{}, false
	}
	// Detection records either the captured text or, for some rules, its
	// converted form; compare the comparison value in whichever form the
	// baseline producer reproduces.
	raw, evaluable, err := evaluateDiffExtractor(corr, producer)
	if !evaluable || err != nil {
		return diffReextraction{}, false
	}
	converted := false
	if raw != baselineValue {
		if convertDiffValue(corr.Convert, raw) != baselineValue {
			return diffReextraction{}, false
		}
		converted = true
	}
	key, template := diffRequestKey(producer), false
	group, peers := a.comparison[key], a.baselineBy[key]
	if len(group) == 0 {
		// A path that carries an earlier dynamic value (a session or resource
		// ID) differs between the recordings; align on the paths with each
		// recording's correlated values masked instead.
		templates := a.templateGroups()
		key, template = templates.baselineKeys[producer.ID], true
		group, peers = templates.comparison[key], templates.baseline[key]
	}
	if len(group) == 0 {
		return diffReextraction{reason: "the comparison HAR has no request with the baseline producer's method, host and path", match: DiffMatchAlignedRequest}, true
	}
	candidates := group
	queryKeys := diffQueryKeySet(producer.RequestURL)
	if narrowed := a.narrow(diffNarrowKey{comparison: true, template: template, group: key, queryKeys: queryKeys}, candidates); len(narrowed) > 0 {
		candidates = narrowed
		peers = a.narrow(diffNarrowKey{template: template, group: key, queryKeys: queryKeys}, peers)
	}
	ordinal := 0
	for index, peer := range peers {
		if peer.ID == producer.ID {
			ordinal = index
			break
		}
	}
	miss := diffReextraction{reason: "the comparison HAR has fewer matching requests than the baseline, so the producer could not be aligned", match: DiffMatchAlignedRequest}
	for attempt, aligned := range a.alignmentOrder(candidates, ordinal, producer.ID) {
		var reason string
		value, err := a.evaluate(corr, aligned)
		switch {
		case err != nil:
			reason = "the extractor did not match the aligned comparison response"
		case value == "":
			reason = "the extractor matched an empty value in the aligned comparison response"
		default:
			if converted {
				value = convertDiffValue(corr.Convert, value)
			}
			if a.reusedAfter(value, aligned.ID) {
				a.claimed[aligned.ID] = producer.ID
				return diffReextraction{entry: aligned.ID, value: value, match: DiffMatchAlignedRequest}, true
			}
			reason = "the extractor matched the aligned comparison response, but the comparison HAR never reuses the value"
		}
		// The response at the producer's position explains a miss best.
		if attempt == 0 && ordinal < len(candidates) {
			miss = diffReextraction{entry: aligned.ID, reason: reason, match: DiffMatchAlignedRequest}
		}
	}
	if namePaired != nil {
		for _, observation := range group {
			if observation.ID == namePaired.FoundInEntry {
				return diffReextraction{entry: observation.ID, value: correlationValue(*namePaired), match: DiffMatchCorrelationName}, true
			}
		}
	}
	return miss, true
}

// narrow is observationsWithQueryKeySet for a request group, once per group and
// query-key set: every producer of a long same-endpoint group narrows it.
func (a *diffAligner) narrow(key diffNarrowKey, observations []sample.HARSourceObservation) []sample.HARSourceObservation {
	if narrowed, ok := a.narrowed[key]; ok {
		return narrowed
	}
	narrowed := observationsWithQueryKeySet(observations, key.queryKeys)
	a.narrowed[key] = narrowed
	return narrowed
}

// evaluate runs corr's extractor on a comparison response, once per extractor
// and response.
func (a *diffAligner) evaluate(corr sample.Correlation, observation sample.HARSourceObservation) (string, error) {
	key := diffExtractionKey{source: corr.Source, extractType: corr.ExtractType, expression: corr.Expression, entry: observation.ID}
	if cached, ok := a.extractions[key]; ok {
		return cached.value, cached.err
	}
	a.evaluations++
	value, _, err := evaluateDiffExtractor(corr, observation)
	a.extractions[key] = diffExtraction{value: value, err: err}
	return value, err
}

// alignmentOrder lists the comparison responses to try for a producer: the
// one at its position among the matching requests first, then the others
// within diffAlignmentWindow of that position in recording order, those
// already aligned with another producer last.
func (a *diffAligner) alignmentOrder(candidates []sample.HARSourceObservation, ordinal, producerID int) []sample.HARSourceObservation {
	if len(candidates) == 0 {
		return nil
	}
	center := min(ordinal, len(candidates)-1)
	low, high := max(0, center-diffAlignmentWindow), min(len(candidates), center+diffAlignmentWindow+1)
	order := make([]sample.HARSourceObservation, 0, high-low)
	if ordinal < len(candidates) {
		order = append(order, candidates[ordinal])
	}
	var claimedByOthers []sample.HARSourceObservation
	for index := low; index < high; index++ {
		if index == ordinal {
			continue
		}
		candidate := candidates[index]
		if owner, ok := a.claimed[candidate.ID]; ok && owner != producerID {
			claimedByOthers = append(claimedByOthers, candidate)
			continue
		}
		order = append(order, candidate)
	}
	return append(order, claimedByOthers...)
}

// reusedAfter reports whether the comparison reuses value: its detection saw
// the value reused, or the value appears verbatim or URL-encoded in a
// comparison request recorded after the producing response.
func (a *diffAligner) reusedAfter(value string, producerID int) bool {
	if a.reusedValues[value] {
		return true
	}
	if len(value) < diffReuseScanMinLength {
		return false
	}
	if a.reuse == nil {
		a.reuse = newDiffReuseIndex(a.requests)
	}
	start := a.reuse.after(producerID)
	if a.reuse.occursFrom(value, start) {
		return true
	}
	if escaped := url.QueryEscape(value); escaped != value {
		return a.reuse.occursFrom(escaped, start)
	}
	return false
}

// diffReuseIndex finds values in the comparison requests (URL, body and
// header values, each searched on its own). A filter of the 4-byte windows of
// all that text rejects most absent values without a scan, and each value
// remembers how far it has been scanned, so a value costs at most one pass
// over the requests however many producers ask about it.
type diffReuseIndex struct {
	// requests are ordered by entry ID.
	requests []sample.HARSourceObservation
	grams    []uint64
	gramBits uint
	scans    map[string]diffReuseScan
	// scanned counts requests searched, for tests.
	scanned int
}

// diffReuseScan is what searching one value established: found is the latest
// request index known to contain it (-1 when none is known), and no request at
// or after clearFrom contains it.
type diffReuseScan struct {
	found, clearFrom int
}

const (
	diffGramMinBits = 12
	// diffGramMaxBits bounds the filter at 8 MiB.
	diffGramMaxBits = 26
)

func newDiffReuseIndex(requests []sample.HARSourceObservation) *diffReuseIndex {
	sorted := append([]sample.HARSourceObservation(nil), requests...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	total := 0
	for _, request := range sorted {
		forEachDiffRequestText(request, func(text string) bool {
			total += len(text)
			return false
		})
	}
	// About eight filter bits per byte of text keeps the filter sparse.
	bits := uint(diffGramMinBits)
	for bits < diffGramMaxBits && 1<<bits < 8*total {
		bits++
	}
	index := &diffReuseIndex{
		requests: sorted,
		grams:    make([]uint64, (1<<bits)/64),
		gramBits: bits,
		scans:    map[string]diffReuseScan{},
	}
	for _, request := range sorted {
		forEachDiffRequestText(request, func(text string) bool {
			for offset := 0; offset+4 <= len(text); offset++ {
				slot := index.gramSlot(text, offset)
				index.grams[slot/64] |= 1 << (slot % 64)
			}
			return false
		})
	}
	return index
}

// forEachDiffRequestText calls visit with each searchable text of a request
// until visit returns true, and reports whether it did.
func forEachDiffRequestText(request sample.HARSourceObservation, visit func(string) bool) bool {
	if visit(request.RequestURL) || visit(request.RequestBody) {
		return true
	}
	for _, header := range request.RequestHeaders {
		if visit(header) {
			return true
		}
	}
	return false
}

func (x *diffReuseIndex) gramSlot(text string, offset int) uint32 {
	gram := uint32(text[offset]) | uint32(text[offset+1])<<8 | uint32(text[offset+2])<<16 | uint32(text[offset+3])<<24
	return (gram * 0x9E3779B1) >> (32 - x.gramBits)
}

// mayContain is false when some 4-byte window of value appears in no request
// text, so no request can contain value.
func (x *diffReuseIndex) mayContain(value string) bool {
	for offset := 0; offset+4 <= len(value); offset++ {
		slot := x.gramSlot(value, offset)
		if x.grams[slot/64]&(1<<(slot%64)) == 0 {
			return false
		}
	}
	return true
}

// after is the index of the first request recorded after entry producerID.
func (x *diffReuseIndex) after(producerID int) int {
	return sort.Search(len(x.requests), func(i int) bool { return x.requests[i].ID > producerID })
}

// occursFrom reports whether a request at index start or later contains value
// (at least diffReuseScanMinLength bytes long).
func (x *diffReuseIndex) occursFrom(value string, start int) bool {
	if start >= len(x.requests) || !x.mayContain(value) {
		return false
	}
	state, ok := x.scans[value]
	if !ok {
		state = diffReuseScan{found: -1, clearFrom: len(x.requests)}
	}
	if state.found >= start {
		return true
	}
	if start >= state.clearFrom {
		return false
	}
	// Only the requests not yet known to be clear need a search.
	for index := start; index < state.clearFrom; index++ {
		x.scanned++
		if forEachDiffRequestText(x.requests[index], func(text string) bool { return strings.Contains(text, value) }) {
			state.found = max(state.found, index)
			x.scans[value] = state
			return true
		}
	}
	state.clearFrom = start
	x.scans[value] = state
	return false
}

// diffTemplateGroups groups each recording's requests by method, host and
// path template: the path with every segment that holds one of the
// recording's correlated values replaced by a placeholder.
type diffTemplateGroups struct {
	baselineKeys         map[int]string
	baseline, comparison map[string][]sample.HARSourceObservation
}

// diffTemplateContainsMinLength is the shortest correlated value a path
// segment may hold inside other text and still be masked; shorter values mask
// only a segment they fill exactly.
const diffTemplateContainsMinLength = 8

func (a *diffAligner) templateGroups() *diffTemplateGroups {
	if a.templates != nil {
		return a.templates
	}
	groups := &diffTemplateGroups{
		baselineKeys: make(map[int]string, len(a.baseline)),
		baseline:     map[string][]sample.HARSourceObservation{},
		comparison:   map[string][]sample.HARSourceObservation{},
	}
	baselineMask := newDiffSegmentMask(a.baselineValues)
	// Group in recording order, as baselineBy does, so ordinals line up.
	for _, observations := range a.baselineBy {
		for _, observation := range observations {
			key := diffTemplateKey(observation, baselineMask)
			groups.baselineKeys[observation.ID] = key
			groups.baseline[key] = append(groups.baseline[key], observation)
		}
	}
	for key := range groups.baseline {
		observations := groups.baseline[key]
		sort.SliceStable(observations, func(i, j int) bool { return observations[i].ID < observations[j].ID })
	}
	comparisonMask := newDiffSegmentMask(a.comparisonValues)
	for _, observation := range a.requests {
		key := diffTemplateKey(observation, comparisonMask)
		groups.comparison[key] = append(groups.comparison[key], observation)
	}
	a.templates = groups
	return groups
}

// diffSegmentMask recognizes path segments that hold a correlated value.
type diffSegmentMask struct {
	exact map[string]bool
	long  []string
}

func newDiffSegmentMask(values []string) diffSegmentMask {
	mask := diffSegmentMask{exact: make(map[string]bool, len(values))}
	for _, value := range values {
		if len(value) < diffReuseScanMinLength || mask.exact[value] {
			continue
		}
		mask.exact[value] = true
		if len(value) >= diffTemplateContainsMinLength {
			mask.long = append(mask.long, value)
		}
	}
	return mask
}

func (m diffSegmentMask) holds(segment string) bool {
	if segment == "" {
		return false
	}
	unescaped, err := url.PathUnescape(segment)
	if err != nil {
		unescaped = segment
	}
	if m.exact[segment] || m.exact[unescaped] {
		return true
	}
	for _, value := range m.long {
		if strings.Contains(unescaped, value) {
			return true
		}
	}
	return false
}

func diffTemplateKey(observation sample.HARSourceObservation, mask diffSegmentMask) string {
	method := strings.ToUpper(strings.TrimSpace(observation.Method))
	parsed, err := url.Parse(observation.RequestURL)
	if err != nil {
		return method + " " + observation.RequestURL
	}
	segments := strings.Split(parsed.EscapedPath(), "/")
	for index, segment := range segments {
		if mask.holds(segment) {
			segments[index] = "{}"
		}
	}
	return method + " " + strings.ToLower(parsed.Host) + strings.Join(segments, "/")
}

func diffRequestKey(observation sample.HARSourceObservation) string {
	method := strings.ToUpper(strings.TrimSpace(observation.Method))
	parsed, err := url.Parse(observation.RequestURL)
	if err != nil {
		return method + " " + observation.RequestURL
	}
	return method + " " + strings.ToLower(parsed.Host) + parsed.EscapedPath()
}

func diffQueryKeySet(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	query, _ := url.ParseQuery(parsed.RawQuery)
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, "&")
}

func observationsWithQueryKeySet(observations []sample.HARSourceObservation, keySet string) []sample.HARSourceObservation {
	var out []sample.HARSourceObservation
	for _, observation := range observations {
		if diffQueryKeySet(observation.RequestURL) == keySet {
			out = append(out, observation)
		}
	}
	return out
}

// evaluateDiffExtractor applies a correlation's extractor to a recorded
// response the way the HTTP runtime does for the extractor kinds correlation
// detection emits, returning the captured text before any convert step.
// evaluable is false for kinds this offline evaluation does not reproduce; err
// reports a miss.
func evaluateDiffExtractor(corr sample.Correlation, observation sample.HARSourceObservation) (value string, evaluable bool, err error) {
	extractType := strings.ToLower(strings.TrimSpace(corr.ExtractType))
	switch strings.ToLower(strings.TrimSpace(corr.Source)) {
	case "header":
		headers := diffResponseHeaders(observation.ResponseHeaders)
		switch extractType {
		case "", "header":
			value = headers.Get(corr.Expression)
			if value == "" {
				err = jsonextract.ErrNotFound
			}
		case "regex":
			value, err = jsonextract.Regex(corr.Expression, []byte(diffFlattenHeaders(headers)))
		case "boundary":
			value, err = jsonextract.Boundary(corr.Expression, []byte(diffFlattenHeaders(headers)))
		default:
			return "", false, nil
		}
	case "", "body":
		body := []byte(observation.ResponseBody)
		switch extractType {
		case "regex":
			value, err = jsonextract.Regex(corr.Expression, body)
		case "jsonpath", "json_path":
			value, err = jsonextract.JSONPath(corr.Expression, body)
		case "jmespath":
			value, err = jsonextract.JMESPath(corr.Expression, body)
		case "boundary":
			value, err = jsonextract.Boundary(corr.Expression, body)
		default:
			return "", false, nil
		}
	default:
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	return value, true, nil
}

// convertDiffValue applies an extractor's convert step as the HTTP runtime does.
func convertDiffValue(convert, value string) string {
	switch strings.ToLower(strings.TrimSpace(convert)) {
	case "url_encode":
		return url.QueryEscape(value)
	case "url_decode":
		if decoded, err := url.QueryUnescape(value); err == nil {
			return decoded
		}
		return value
	case "html_decode":
		return html.UnescapeString(value)
	case "html_url_encode":
		return strings.ReplaceAll(url.QueryEscape(html.UnescapeString(value)), "+", "%20")
	default:
		return value
	}
}

// diffResponseHeaders rebuilds the response header set from an observation,
// whose repeated values are newline-delimited.
func diffResponseHeaders(headers map[string]string) http.Header {
	out := make(http.Header, len(headers))
	for name, value := range headers {
		for _, part := range strings.Split(value, "\n") {
			out.Add(name, part)
		}
	}
	return out
}

// diffFlattenHeaders is the runtime's header text surface for regex and
// boundary extractors: "Name: value" lines sorted by header name.
func diffFlattenHeaders(headers http.Header) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var builder strings.Builder
	for _, name := range names {
		for _, value := range headers.Values(name) {
			builder.WriteString(name)
			builder.WriteString(": ")
			builder.WriteString(value)
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func correlationDiffKey(corr sample.Correlation) string {
	// Include the (uniquified) correlation Name so two distinct correlations that
	// share source/expression/extract_type — e.g. a cart-id and an order-id both
	// extracted via $.id from different steps — do not collide in the comparison
	// map (the last would overwrite the first, comparing every same-keyed
	// baseline correlation against one arbitrary value). Name is assigned in
	// detection order (corr_id / corr_id_2), so it shifts when one recording
	// loses or gains a producer; that is why this pairing is only the fallback
	// for extractors DiffPrepared cannot re-evaluate on an aligned request.
	return fmt.Sprintf("%s|%s|%s|%s",
		strings.ToLower(strings.TrimSpace(corr.Source)),
		strings.ToLower(strings.TrimSpace(corr.Expression)),
		strings.ToLower(strings.TrimSpace(corr.ExtractType)),
		strings.ToLower(strings.TrimSpace(corr.Name)))
}

func correlationValue(corr sample.Correlation) string {
	if strings.TrimSpace(corr.Value) != "" {
		return corr.Value
	}
	return corr.SampleValue
}

func publicCorrelationValue(corr sample.Correlation) string {
	return corr.SampleValue
}

func diffRank(classification string) int {
	switch classification {
	case DiffClassificationDynamic:
		return 0
	case DiffClassificationCandidate:
		return 1
	case DiffClassificationStatic:
		return 2
	default:
		return 3
	}
}

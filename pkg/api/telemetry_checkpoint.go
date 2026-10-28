package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

const maxRetainedTelemetryCollections = 1024
const maxRetainedTelemetryWindowEntries = 4096

const (
	telemetryCollectionClassificationUnknown                  = "unknown"
	telemetryCollectionClassificationQualifiedSourceWindows   = "qualified_source_windows"
	telemetryCollectionClassificationUnscoredBoundarySpanning = "unscored_boundary_spanning"
	telemetryCollectionClassificationUnqualifiedClock         = "unqualified_clock"
)

func validTelemetryCollectionClassification(classification string) bool {
	switch classification {
	case telemetryCollectionClassificationUnknown,
		telemetryCollectionClassificationQualifiedSourceWindows,
		telemetryCollectionClassificationUnscoredBoundarySpanning,
		telemetryCollectionClassificationUnqualifiedClock:
		return true
	default:
		return false
	}
}

// TelemetryCollectionCounter preserves a cumulative source reading and its availability.
type TelemetryCollectionCounter struct {
	Value       *int64 `json:"value,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

// TelemetryCollectionEvidence retains source-clock provenance and, when the
// generator's clock is qualified and the uncertainty-expanded window falls
// wholly inside one policy segment, its qualified controller-clock segment
// classification (TEL-A2).
type TelemetryCollectionEvidence struct {
	CounterReadings map[string]TelemetryCollectionCounter `json:"counter_readings,omitempty"`

	Source          string                                 `json:"source"`
	Generation      uint64                                 `json:"execution_generation"`
	Epoch           string                                 `json:"source_epoch"`
	Collection      uint64                                 `json:"collection_sequence"`
	Publication     uint64                                 `json:"publication_sequence"`
	Windows         []generatorrpc.MetricsCollectionWindow `json:"windows,omitempty"`
	Complete        bool                                   `json:"windows_complete"`
	PolicySegment   int64                                  `json:"policy_segment,omitempty"`
	PolicyHash      string                                 `json:"policy_hash,omitempty"`
	Classification  string                                 `json:"classification"`
	ClockReason     string                                 `json:"clock_reason,omitempty"`
	UncertaintyMs   *float64                               `json:"uncertainty_ms,omitempty"`
	ControllerStart *time.Time                             `json:"controller_start,omitempty"`
	ControllerEnd   *time.Time                             `json:"controller_end,omitempty"`
}

type durableWindowMark struct {
	Generation     uint64
	EpochStartedMs int64
	Epoch          string
	Collection     uint64
	PublishedAt    int64
	BatchSequence  uint64
	BatchTimestamp int64
}

func durableMark(m distributedWindowMark) durableWindowMark {
	return durableWindowMark{m.generation, m.epochStartedMs, m.epoch, m.collection, m.publishedAt, m.batchSequence, m.batchTimestamp}
}
func (m durableWindowMark) restore() distributedWindowMark {
	return distributedWindowMark{m.Generation, m.EpochStartedMs, m.Epoch, m.Collection, m.PublishedAt, m.BatchSequence, m.BatchTimestamp}
}

type durableCounterTrack struct {
	Usable, Invalid, Absent        int64
	ProcessWideOnly                bool
	Max                            int64
	LastSequence                   uint64
	Last                           int64
	Unordered, Regressed, Terminal bool
	TerminalValue                  int64
}
type durableSourceEpoch struct {
	Record   TelemetrySourceEpoch
	Ranges   [][2]uint64
	Counters map[string]durableCounterTrack
}
type durableCoverage struct {
	Source                   string
	Generation               uint64
	Epoch                    string
	EpochStartedMs           int64
	Sequence, WindowSequence uint64
	EndMs                    int64
	Collection               durableWindowMark
	Terminal                 bool
}
type durableSpans struct {
	Spans     [][2]int64
	Truncated bool
}
type durableJointStream struct {
	Source              string
	ExecutionGeneration uint64
	Epoch               string
	AcceptedRanges      [][2]uint64           `json:",omitempty"`
	IgnoredRanges       [][2]uint64           `json:",omitempty"`
	DroppedRanges       [][2]uint64           `json:",omitempty"`
	Windows             []TelemetryJointWindow `json:",omitempty"`
	DroppedWindows      []TelemetryJointWindow `json:",omitempty"`
	FirstStartUnixMs    int64                  `json:",omitempty"`
	LastEndUnixMs       int64                  `json:",omitempty"`
	Truncated           bool                   `json:",omitempty"`
}

type durableRenderPopulation struct {
	ReferenceClientID string                       `json:"reference_client_id,omitempty"`
	Epoch             string                       `json:"epoch,omitempty"`
	FirstSequence     uint64                       `json:"first_sequence,omitempty"`
	LastSequence      uint64                       `json:"last_sequence,omitempty"`
	PublishedEligible int64                        `json:"published_eligible"`
	Eligible          int64                        `json:"eligible"`
	Rendered          int64                        `json:"rendered"`
	Superseded        int64                        `json:"superseded"`
	Unqualified       map[string]int64             `json:"unqualified,omitempty"`
	Unreported        int64                        `json:"unreported"`
	Ineligible        int64                        `json:"ineligible,omitempty"`
	Unknown           int64                        `json:"unknown,omitempty"`
	SampledFrom       *time.Time                   `json:"sampled_from,omitempty"`
	PriorEpochs       []TelemetryRenderPopulation  `json:"prior_epochs,omitempty"`
	EndToEndDigest    *engine.TDigestSnapshot      `json:"end_to_end_digest,omitempty"`
	EndToEndMax       float64                      `json:"end_to_end_max,omitempty"`
	EndToEndExcluded  map[string]int64             `json:"end_to_end_excluded,omitempty"`
	NewestDigest      *engine.TDigestSnapshot      `json:"newest_digest,omitempty"`
	NewestMax         float64                      `json:"newest_max,omitempty"`
	DeliverySignal    string                       `json:"delivery_signal,omitempty"`
}

func validDurableRenderPopulation(d *durableRenderPopulation) bool {
	if d == nil {
		return true
	}
	if d.PublishedEligible < 0 || d.Eligible < 0 || d.Rendered < 0 || d.Superseded < 0 || d.Unreported < 0 || d.Ineligible < 0 || d.Unknown < 0 {
		return false
	}
	if d.FirstSequence > 0 && d.LastSequence < d.FirstSequence {
		return false
	}
	for _, v := range d.Unqualified {
		if v < 0 {
			return false
		}
	}
	for _, v := range d.EndToEndExcluded {
		if v < 0 {
			return false
		}
	}
	return true
}

func captureDurableRenderPopulation(r *telemetryStageRecorder) *durableRenderPopulation {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pop := r.renderPopulationLocked()
	e2e := r.stages[telemetryStageEndToEnd]
	if pop == nil && (e2e == nil || (e2e.digest.Count() == 0 && len(e2e.excluded) == 0)) {
		return nil
	}
	out := &durableRenderPopulation{DeliverySignal: r.endToEndSignal}
	if pop != nil {
		out.ReferenceClientID = pop.ReferenceClient
		out.Epoch = pop.Epoch
		out.FirstSequence = pop.FirstSequence
		out.LastSequence = pop.LastSequence
		out.PublishedEligible = pop.PublishedEligible
		out.Eligible = pop.Eligible
		out.Rendered = pop.Rendered
		out.Superseded = pop.Superseded
		out.Unqualified = pop.Unqualified
		out.Unreported = pop.Unreported
		out.Ineligible = pop.Ineligible
		out.Unknown = pop.Unknown
		out.SampledFrom = pop.SampledFrom
		out.PriorEpochs = pop.PriorEpochs
	}
	if e2e != nil {
		if e2e.digest.Count() > 0 {
			snap := e2e.digest.Snapshot()
			out.EndToEndDigest = &snap
			out.EndToEndMax = e2e.max
		}
		if len(e2e.excluded) > 0 {
			out.EndToEndExcluded = make(map[string]int64, len(e2e.excluded))
			for k, v := range e2e.excluded {
				out.EndToEndExcluded[k] = v
			}
		}
	}
	if r.endToEndNewest != nil && r.endToEndNewest.digest.Count() > 0 {
		snap := r.endToEndNewest.digest.Snapshot()
		out.NewestDigest = &snap
		out.NewestMax = r.endToEndNewest.max
	}
	return out
}

func restoreDurableRenderPopulation(r *telemetryStageRecorder, d *durableRenderPopulation) {
	if r == nil || d == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var priors []TelemetryRenderPopulation
	for i := range d.PriorEpochs {
		priors = append(priors, *d.PriorEpochs[i].clone())
	}
	if d.Epoch != "" || d.PublishedEligible > 0 || d.Eligible > 0 || d.ReferenceClientID != "" {
		unq := map[string]int64{}
		for k, v := range d.Unqualified {
			unq[k] = v
		}
		if len(unq) == 0 {
			unq = nil
		}
		var sampledFrom *time.Time
		if d.SampledFrom != nil {
			t := d.SampledFrom.UTC()
			sampledFrom = &t
		}
		unreported := d.Unreported
		if d.PublishedEligible > d.Eligible {
			unreported += d.PublishedEligible - d.Eligible
		}
		priors = append(priors, TelemetryRenderPopulation{
			Population:        telemetryRenderPopulationName,
			Epoch:             d.Epoch,
			PublishedEligible: d.PublishedEligible,
			ReferenceClient:   d.ReferenceClientID,
			FirstSequence:     d.FirstSequence,
			LastSequence:      d.LastSequence,
			Eligible:          d.Eligible,
			Rendered:          d.Rendered,
			Superseded:        d.Superseded,
			Unqualified:       unq,
			Unreported:        unreported,
			Ineligible:        d.Ineligible,
			Unknown:           d.Unknown,
			SampledFrom:       sampledFrom,
		})
	}
	r.render.priorEpochs = priors
	if d.EndToEndDigest != nil && d.EndToEndDigest.Count > 0 {
		stage := r.stageLocked(telemetryStageEndToEnd)
		stage.digest.Merge(engine.NewTDigestFromSnapshot(*d.EndToEndDigest))
		stage.max = max(stage.max, d.EndToEndMax)
	}
	for reason, count := range d.EndToEndExcluded {
		if count > 0 {
			stage := r.stageLocked(telemetryStageEndToEnd)
			if stage.excluded == nil {
				stage.excluded = map[string]int64{}
			}
			stage.excluded[reason] += count
		}
	}
	if d.NewestDigest != nil && d.NewestDigest.Count > 0 {
		if r.endToEndNewest == nil {
			r.endToEndNewest = &telemetryStageDigest{digest: engine.NewTDigest(telemetryStageCompression)}
		}
		r.endToEndNewest.digest.Merge(engine.NewTDigestFromSnapshot(*d.NewestDigest))
		r.endToEndNewest.max = max(r.endToEndNewest.max, d.NewestMax)
	}
	if d.DeliverySignal != "" {
		r.endToEndSignal = mergeDeliverySignal(r.endToEndSignal, d.DeliverySignal)
	}
}

type telemetryCheckpoint struct {
	Version               int
	Incarnation           string
	Generation            uint64
	Revision              int64
	Totals                [4]int64
	Groups                map[string][4]int64
	RequestsUnknown       bool
	Windows               map[string]durableWindowMark
	Epochs                []durableSourceEpoch
	JointStreams          []durableJointStream `json:",omitempty"`
	JointStreamsTruncated bool                 `json:",omitempty"`
	Coverage              map[string]durableSpans
	Requirements          map[string][2]int64
	Cursors               []durableCoverage
	Collections           []TelemetryCollectionEvidence
	CollectionsTruncated  bool
	Truncated, Adopted    bool
	RenderPopulation      *durableRenderPopulation `json:"RenderPopulation,omitempty"`
}

func durableCounts(c runTotalsCounts) [4]int64 {
	return [4]int64{c.tx, c.errors, c.requests, c.requestErrors}
}
func restoredCounts(c [4]int64) runTotalsCounts { return runTotalsCounts{c[0], c[1], c[2], c[3]} }

// captureTelemetryCheckpointLocked snapshots cursor and effects under tr.mu.
// Network/storage I/O occurs after unlocking. Publication-delivery marks are
// deliberately not restored: durable collection is not proof of live delivery.
func captureTelemetryCheckpointLocked(test *ManagedTest) telemetryCheckpoint {
	test.telemetryCheckpointRevision++
	a := &test.runTotals
	cp := telemetryCheckpoint{Version: 1, Incarnation: managedTestRunIncarnation(test), Generation: test.executionGeneration, Revision: test.telemetryCheckpointRevision,
		Totals: durableCounts(a.total), Groups: map[string][4]int64{}, RequestsUnknown: a.requestsUnknown, Windows: map[string]durableWindowMark{},
		Coverage: map[string]durableSpans{}, Requirements: map[string][2]int64{}}
	for k, v := range a.groups {
		cp.Groups[k] = durableCounts(v)
	}
	for k, v := range a.windows {
		cp.Windows[k] = durableMark(v)
	}
	l := telemetrySourceLedgerLocked(test)
	l.mu.Lock()
	defer l.mu.Unlock()
	cp.Truncated, cp.Adopted = l.truncated, l.adopted
	cp.CollectionsTruncated = l.collectionsTruncated
	cp.Collections = cloneTelemetryCollections(l.collections)
	for _, r := range l.epochs {
		record := *r
		record.counters = nil
		record.ranges = nil
		record.CollectionWindows = nil
		d := durableSourceEpoch{Record: record, Ranges: append([][2]uint64(nil), r.ranges...), Counters: map[string]durableCounterTrack{}}
		for name, c := range r.counters {
			d.Counters[name] = durableCounterTrack{c.usable, c.invalid, c.absent, c.processWideOnly, c.max, c.lastSequence, c.last, c.unordered, c.regressed, c.terminal, c.terminalValue}
		}
		cp.Epochs = append(cp.Epochs, d)
	}
	sort.Slice(cp.Epochs, func(i, j int) bool {
		a, b := cp.Epochs[i].Record, cp.Epochs[j].Record
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.ExecutionGeneration != b.ExecutionGeneration {
			return a.ExecutionGeneration < b.ExecutionGeneration
		}
		return a.Epoch < b.Epoch
	})
	for source, c := range l.coverage {
		cp.Coverage[source] = durableSpans{append([][2]int64(nil), c.spans...), c.truncated}
	}
	for source, r := range l.requirements {
		cp.Requirements[source] = [2]int64{r.assignedAtMs, r.retiredAtMs}
	}
	for source, c := range l.cursors {
		cp.Cursors = append(cp.Cursors, durableCoverage{source, c.key.generation, c.key.epoch, c.epochStartedMs, c.sequence, c.windowSequence, c.endMs, durableMark(c.collection), c.terminal})
	}
	sort.Slice(cp.Cursors, func(i, j int) bool { return cp.Cursors[i].Source < cp.Cursors[j].Source })
	cp.JointStreamsTruncated = l.jointStreamsTruncated
	for _, s := range l.jointStreams {
		cp.JointStreams = append(cp.JointStreams, durableJointStream{
			Source:              s.Source,
			ExecutionGeneration: s.ExecutionGeneration,
			Epoch:               s.Epoch,
			AcceptedRanges:      append([][2]uint64(nil), s.AcceptedRanges...),
			IgnoredRanges:       append([][2]uint64(nil), s.IgnoredRanges...),
			DroppedRanges:       append([][2]uint64(nil), s.DroppedRanges...),
			Windows:             append([]TelemetryJointWindow(nil), s.Windows...),
			DroppedWindows:      append([]TelemetryJointWindow(nil), s.DroppedWindows...),
			FirstStartUnixMs:    s.FirstStartUnixMs,
			LastEndUnixMs:       s.LastEndUnixMs,
			Truncated:           s.Truncated,
		})
	}
	sort.Slice(cp.JointStreams, func(i, j int) bool {
		a, b := cp.JointStreams[i], cp.JointStreams[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.ExecutionGeneration != b.ExecutionGeneration {
			return a.ExecutionGeneration < b.ExecutionGeneration
		}
		return a.Epoch < b.Epoch
	})
	cp.RenderPopulation = captureDurableRenderPopulation(test.telemetryStages)
	return cp
}

// telemetryCheckpointWithinBounds is shared by writers and readers so a
// growing ledger cannot replace the last recoverable snapshot with one the
// successor would refuse. Never trim a cursor independently from its effects.
func telemetryCheckpointWithinBounds(cp *telemetryCheckpoint) bool {
	if len(cp.Epochs) > maxTelemetrySourceEpochs || len(cp.JointStreams) > maxTelemetrySourceEpochs || len(cp.Collections) > maxRetainedTelemetryCollections ||
		len(cp.Windows) > maxTelemetrySourceEpochs || len(cp.Groups) > maxTelemetrySourceEpochs ||
		len(cp.Coverage) > maxTelemetrySourceEpochs || len(cp.Requirements) > maxTelemetrySourceEpochs || len(cp.Cursors) > maxTelemetrySourceEpochs {
		return false
	}
	for _, js := range cp.JointStreams {
		if len(js.AcceptedRanges) > maxTelemetrySourceRanges || len(js.IgnoredRanges) > maxTelemetrySourceRanges || len(js.DroppedRanges) > maxTelemetrySourceRanges ||
			len(js.Windows) > maxTelemetrySourceRanges || len(js.DroppedWindows) > maxTelemetrySourceRanges {
			return false
		}
	}
	entries := 0
	for _, c := range cp.Collections {
		entries += len(c.Windows)
		if len(c.Windows) > generatorrpc.MaxMetricsCollectionWindows || entries > maxRetainedTelemetryWindowEntries {
			return false
		}
	}
	return true
}

// errTelemetryCheckpointSuperseded marks a compatible snapshot written before the
// last term that armed run continuity. That term may have counted windows the
// snapshot lacks, so recovery uses stored history and may replace the payload.
var errTelemetryCheckpointSuperseded = errors.New("telemetry checkpoint predates the adopted execution term")

// minGeneration is the execution term of the continuity checkpoint being adopted.
func decodeTelemetryCheckpoint(raw string, revision int64, test *ManagedTest, minGeneration uint64) (*telemetrySourceLedger, runTotalsAccumulator, error) {
	var cp telemetryCheckpoint
	var a runTotalsAccumulator
	if len(raw) == 0 || len(raw) > storage.MaxTelemetryCheckpointBytes {
		return nil, a, fmt.Errorf("missing or oversized checkpoint")
	}
	// All top-level fields belong to version 1. Missing zero-valued counters
	// or completeness flags must not turn a partial payload into measured zero.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, a, err
	}
	for _, name := range []string{"Version", "Incarnation", "Generation", "Revision", "Totals", "Groups", "RequestsUnknown", "Windows", "Epochs", "Coverage", "Requirements", "Cursors", "Collections", "CollectionsTruncated", "Truncated", "Adopted"} {
		if _, ok := fields[name]; !ok {
			return nil, a, fmt.Errorf("missing checkpoint field %s", name)
		}
	}
	var totals []int64
	if err := json.Unmarshal(fields["Totals"], &totals); err != nil || len(totals) != 4 {
		return nil, a, fmt.Errorf("invalid checkpoint totals")
	}
	for _, name := range []string{"RequestsUnknown", "CollectionsTruncated", "Truncated", "Adopted"} {
		if string(fields[name]) != "true" && string(fields[name]) != "false" {
			return nil, a, fmt.Errorf("invalid checkpoint flag %s", name)
		}
	}
	if rawFlag, ok := fields["JointStreamsTruncated"]; ok && string(rawFlag) != "true" && string(rawFlag) != "false" {
		return nil, a, fmt.Errorf("invalid checkpoint flag JointStreamsTruncated")
	}
	if err := json.Unmarshal([]byte(raw), &cp); err != nil {
		return nil, a, err
	}
	if cp.Version != 1 || cp.Revision != revision || revision <= 0 ||
		cp.Incarnation == "" || cp.Incarnation != managedTestRunIncarnation(test) || cp.Generation > test.executionGeneration ||
		cp.Windows == nil || cp.Groups == nil || cp.Coverage == nil || cp.Requirements == nil ||
		!telemetryCheckpointWithinBounds(&cp) || !validDurableRenderPopulation(cp.RenderPopulation) {
		return nil, a, fmt.Errorf("incompatible telemetry checkpoint")
	}
	if cp.Generation < minGeneration {
		return nil, a, errTelemetryCheckpointSuperseded
	}
	l := newTelemetrySourceLedger()
	l.restoredRenderPopulation = cp.RenderPopulation
	l.truncated, l.adopted = cp.Truncated, cp.Adopted
	l.jointStreamsTruncated = cp.JointStreamsTruncated
	l.collections, l.collectionsTruncated = cp.Collections, cp.CollectionsTruncated
	l.coverage = map[string]*coverageSpans{}
	l.requirements = map[string]*telemetrySourceRequirement{}
	l.cursors = map[string]*coverageCursor{}
	for _, js := range cp.JointStreams {
		key := telemetrySourceKey{js.Source, js.ExecutionGeneration, js.Epoch}
		if js.Source == "" || js.ExecutionGeneration > cp.Generation || l.jointStreams[key] != nil || js.FirstStartUnixMs < 0 || js.LastEndUnixMs < 0 || (js.FirstStartUnixMs > 0 && js.LastEndUnixMs < js.FirstStartUnixMs) {
			return nil, a, fmt.Errorf("invalid joint stream ledger")
		}
		for _, ranges := range [][][2]uint64{js.AcceptedRanges, js.IgnoredRanges, js.DroppedRanges} {
			for i, span := range ranges {
				if span[0] == 0 || span[1] < span[0] || i > 0 && span[0] <= ranges[i-1][1] {
					return nil, a, fmt.Errorf("invalid joint stream ranges")
				}
			}
		}
		for _, windows := range [][]TelemetryJointWindow{js.Windows, js.DroppedWindows} {
			for i, w := range windows {
				if w.LastSequence < w.FirstSequence || w.StartUnixMs < 0 || w.EndUnixMs < w.StartUnixMs || i > 0 && w.FirstSequence > 0 && windows[i-1].FirstSequence > 0 && w.FirstSequence <= windows[i-1].LastSequence {
					return nil, a, fmt.Errorf("invalid joint stream windows")
				}
			}
		}
		l.jointStreams[key] = &telemetryJointStream{
			Source:              js.Source,
			ExecutionGeneration: js.ExecutionGeneration,
			Epoch:               js.Epoch,
			AcceptedRanges:      append([][2]uint64(nil), js.AcceptedRanges...),
			IgnoredRanges:       append([][2]uint64(nil), js.IgnoredRanges...),
			DroppedRanges:       append([][2]uint64(nil), js.DroppedRanges...),
			Windows:             append([]TelemetryJointWindow(nil), js.Windows...),
			DroppedWindows:      append([]TelemetryJointWindow(nil), js.DroppedWindows...),
			FirstStartUnixMs:    js.FirstStartUnixMs,
			LastEndUnixMs:       js.LastEndUnixMs,
			Truncated:           js.Truncated,
		}
	}
	for _, d := range cp.Epochs {
		r := d.Record
		key := telemetrySourceKey{r.Source, r.ExecutionGeneration, r.Epoch}
		if r.Source == "" || r.ExecutionGeneration > cp.Generation || l.epochs[key] != nil || len(d.Ranges) > maxTelemetrySourceRanges || len(d.Counters) > len(telemetryEpochCounters) {
			return nil, a, fmt.Errorf("invalid source ledger")
		}
		for i, span := range d.Ranges {
			if span[0] == 0 || span[1] < span[0] || i > 0 && span[0] <= d.Ranges[i-1][1] {
				return nil, a, fmt.Errorf("invalid publication ranges")
			}
		}
		r.CollectionWindows = nil
		r.ranges = d.Ranges
		r.counters = map[string]*telemetryCounterTrack{}
		for name, c := range d.Counters {
			if c.Usable < 0 || c.Invalid < 0 || c.Absent < 0 || c.Max < 0 || c.Last < 0 || c.TerminalValue < 0 {
				return nil, a, fmt.Errorf("invalid counter track")
			}
			r.counters[name] = &telemetryCounterTrack{c.Usable, c.Invalid, c.Absent, c.ProcessWideOnly, c.Max, c.LastSequence, c.Last, c.Unordered, c.Regressed, c.Terminal, c.TerminalValue}
		}
		l.epochs[key] = &r
	}
	for source, d := range cp.Coverage {
		if len(d.Spans) > maxTelemetryCoverageSpans {
			return nil, a, fmt.Errorf("oversized coverage")
		}
		for i, s := range d.Spans {
			if s[1] <= s[0] || i > 0 && s[0] <= d.Spans[i-1][1] {
				return nil, a, fmt.Errorf("invalid coverage")
			}
		}
		l.coverage[source] = &coverageSpans{d.Spans, d.Truncated}
	}
	for source, r := range cp.Requirements {
		l.requirements[source] = &telemetrySourceRequirement{r[0], r[1]}
	}
	for _, c := range cp.Cursors {
		if l.cursors[c.Source] != nil || c.Generation > cp.Generation {
			return nil, a, fmt.Errorf("invalid cursor")
		}
		l.cursors[c.Source] = &coverageCursor{telemetrySourceKey{c.Source, c.Generation, c.Epoch}, c.EpochStartedMs, c.Sequence, c.WindowSequence, c.EndMs, c.Collection.restore(), c.Terminal}
	}
	entries := 0
	for _, c := range cp.Collections {
		entries += len(c.Windows)
		if !validTelemetryCollectionClassification(c.Classification) || len(c.CounterReadings) > len(telemetryEpochCounters) || entries > maxRetainedTelemetryWindowEntries || len(c.Windows) > generatorrpc.MaxMetricsCollectionWindows || c.Generation > cp.Generation {
			return nil, a, fmt.Errorf("invalid source windows")
		}
		if c.Complete && len(c.Windows) == 0 {
			return nil, a, fmt.Errorf("missing complete windows")
		}
		seen := map[string]bool{}
		for _, w := range c.Windows {
			if strings.TrimSpace(w.Group) == "" || len(w.Group) > 512 || seen[w.Group] || w.Start.IsZero() || !w.End.After(w.Start) {
				return nil, a, fmt.Errorf("invalid window boundary")
			}
			seen[w.Group] = true
		}
		for _, r := range c.CounterReadings {
			if r.Value != nil && (*r.Value < 0 || r.Unavailable != "") || r.Value == nil && r.Unavailable == "" {
				return nil, a, fmt.Errorf("invalid counter reading")
			}
		}
	}
	for _, v := range cp.Totals {
		if v < 0 {
			return nil, a, fmt.Errorf("invalid totals")
		}
	}
	for _, counts := range cp.Groups {
		for _, v := range counts {
			if v < 0 {
				return nil, a, fmt.Errorf("invalid group totals")
			}
		}
	}
	a.initialized, a.distributed = true, true
	a.total = restoredCounts(cp.Totals)
	a.requestsUnknown = cp.RequestsUnknown
	a.groups = map[string]runTotalsCounts{}
	a.windows = map[string]distributedWindowMark{}
	for k, v := range cp.Groups {
		a.groups[k] = restoredCounts(v)
	}
	for k, v := range cp.Windows {
		a.windows[k] = v.restore()
	}
	return l, a, nil
}

type telemetryCheckpointStore interface {
	SaveTelemetryCheckpoint(storage.TelemetryCheckpointWrite) (bool, error)
	LoadTelemetryCheckpoint(string, string, string, time.Time) (string, int64, error)
}

func (tr *TestRunner) persistTelemetryCheckpoint(test *ManagedTest) {
	store, ok := tr.historyStore.(telemetryCheckpointStore)
	if !ok {
		return
	}
	tr.mu.Lock()
	// Only a continue-policy run arms continuity and can be adopted, which is
	// the sole reader of this snapshot. Other runs skip the per-batch copy and write.
	if managedTestRunIncarnation(test) == "" || tr.tests[test.ID] != test || !tr.holdsExecutionLeadership() || !test.runTotals.initialized || test.telemetryCheckpointReadOnly ||
		effectiveFailoverPolicy(test.Assignment) != failoverPolicyContinue {
		tr.mu.Unlock()
		return
	}
	cp := captureTelemetryCheckpointLocked(test)
	w := storage.TelemetryCheckpointWrite{ID: test.ID, RunIncarnation: managedTestRunIncarnation(test), RefKey: test.RefKey, ProjectID: normalizeProjectID(test.ProjectID), StartTime: test.CreatedAt, Generation: test.executionGeneration, Revision: cp.Revision, LeaseOwnerID: test.executionFence}
	if w.LeaseOwnerID != "" {
		w.LeaseName = executionLeaseName
	}
	tr.mu.Unlock()
	if !telemetryCheckpointWithinBounds(&cp) {
		slog.Warn("telemetry checkpoint exceeds retained ledger limits; previous snapshot preserved", "test_id", w.ID)
		return
	}
	encoded, err := json.Marshal(cp)
	if err == nil {
		w.Payload = string(encoded)
		_, err = store.SaveTelemetryCheckpoint(w)
	}
	if err != nil {
		slog.Warn("telemetry checkpoint not committed; recovery retains earlier evidence", "test_id", test.ID, "error", err)
	}
}

// Called only after the successor has secured the generators, before streams
// resume. Never combine a restored cursor with totals reconstructed from history.
func (tr *TestRunner) restoreTelemetryCheckpoint(test *ManagedTest, generation, priorGeneration uint64) {
	store, ok := tr.historyStore.(telemetryCheckpointStore)
	if !ok {
		return
	}
	tr.mu.RLock()
	identity := ManagedTest{ID: test.ID, RefKey: test.RefKey, ProjectID: test.ProjectID, CreatedAt: test.CreatedAt, executionGeneration: generation, initialArchiveProvenance: &RunArchiveProvenance{RunIncarnation: managedTestRunIncarnation(test)}}
	originalGeneration := test.executionGeneration
	tr.mu.RUnlock()
	raw, revision, loadErr := store.LoadTelemetryCheckpoint(identity.ID, identity.RefKey, normalizeProjectID(identity.ProjectID), identity.CreatedAt)
	if loadErr != nil {
		slog.Warn("telemetry checkpoint read failed", "test_id", identity.ID, "error", loadErr)
	}
	ledger, totals, err := decodeTelemetryCheckpoint(raw, revision, &identity, priorGeneration)
	if loadErr != nil {
		err = loadErr
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[test.ID] != test || test.executionGeneration != originalGeneration {
		return
	}
	test.telemetryCheckpointReadOnly = loadErr != nil
	test.telemetryCheckpointRestored = false
	test.telemetrySources = nil
	test.runTotals = runTotalsAccumulator{}
	if raw == "" {
		return
	}
	// Even an unknown future payload must not be overwritten by an older writer.
	if revision > test.telemetryCheckpointRevision {
		test.telemetryCheckpointRevision = revision
	}
	if errors.Is(err, errTelemetryCheckpointSuperseded) {
		slog.Info("telemetry checkpoint predates the adopted execution term; recovering from stored history", "test_id", test.ID)
		return
	}
	if err != nil {
		test.telemetryCheckpointReadOnly = true
		slog.Warn("telemetry checkpoint unavailable for recovery", "test_id", test.ID, "error", err)
		return
	}
	test.telemetrySources = ledger
	test.runTotals = totals
	test.telemetryCheckpointRestored = true
	if ledger != nil && ledger.restoredRenderPopulation != nil {
		restoreDurableRenderPopulation(stageRecorderLocked(test), ledger.restoredRenderPopulation)
	}
}

func retainTelemetryCollectionLocked(test *ManagedTest, target string, batch generatorrpc.MetricsBatch) {
	if batch.Telemetry == nil || batch.Telemetry.CollectionSequence == 0 {
		return
	}
	e := TelemetryCollectionEvidence{Source: target, Generation: batch.ExecutionGeneration, Epoch: batch.SourceEpoch, Collection: batch.Telemetry.CollectionSequence, Publication: batch.BatchSequence, Complete: batch.CollectionWindowsComplete, Classification: telemetryCollectionClassificationUnknown}
	now := time.Now()
	offsetMs := batch.Telemetry.CollectedAtOffsetMs
	quality := batch.Telemetry.CollectedAtQuality
	if quality == nil && test != nil && test.clockCalibrations != nil {
		evalNow := now
		if !batch.Telemetry.ReceivedAt.IsZero() {
			evalNow = batch.Telemetry.ReceivedAt
		}
		off, q := test.clockCalibrations.quality(target, evalNow)
		offsetMs, quality = off, &q
	}
	windows := batch.CollectionWindows
	if len(windows) == 0 {
		bt := *batch.Telemetry
		if bt.WindowMs == 0 && batch.WindowMs > 0 {
			bt.WindowMs = batch.WindowMs
		}
		if w, ok := generatorrpc.MetricsCollectionWindowFromTelemetry(&bt, batch.TimestampUnixMs); ok {
			windows = []generatorrpc.MetricsCollectionWindow{w}
		}
	}
	segments := telemetryPolicySegmentsLocked(test)
	switch {
	case quality != nil && quality.Qualified && len(windows) > 0:
		minStart, maxEnd := windows[0].Start, windows[0].End
		for _, w := range windows[1:] {
			if w.Start.Before(minStart) {
				minStart = w.Start
			}
			if w.End.After(maxEnd) {
				maxEnd = w.End
			}
		}
		offsetDur := time.Duration(math.Round(offsetMs * float64(time.Microsecond) * 1000))
		uncertaintyDur := time.Duration(math.Ceil(quality.UncertaintyMs * float64(time.Microsecond) * 1000))
		ctrlStart := minStart.Add(-offsetDur - uncertaintyDur).UTC()
		ctrlEnd := maxEnd.Add(-offsetDur + uncertaintyDur).UTC()
		uCopy := quality.UncertaintyMs
		e.ControllerStart = &ctrlStart
		e.ControllerEnd = &ctrlEnd
		e.UncertaintyMs = &uCopy
		interval := telemetryIntervalPolicyLocked(test, ctrlStart, ctrlEnd)
		if interval.spanning {
			e.Classification = telemetryCollectionClassificationUnscoredBoundarySpanning
			e.PolicyHash = ""
			e.PolicySegment = 0
		} else {
			e.Classification = telemetryCollectionClassificationQualifiedSourceWindows
			if interval.ok && interval.segment.Policy != nil {
				e.PolicySegment = interval.segment.Sequence
				e.PolicyHash = interval.segment.Policy.Hash
			}
		}
	case (quality != nil && !quality.Qualified) || (len(segments) > 1 && (quality == nil || !quality.Qualified)):
		e.Classification = telemetryCollectionClassificationUnqualifiedClock
		e.PolicyHash = ""
		e.PolicySegment = 0
		if quality != nil && quality.Reason != "" {
			e.ClockReason = quality.Reason
		} else {
			e.ClockReason = clockReasonUncalibrated
		}
	default:
		measured := telemetryMeasurement{collectedAt: time.UnixMilli(batch.Telemetry.CollectedAtUnixMs), offsetMs: offsetMs, quality: quality}
		if batch.Telemetry.CollectedAtUnixMs <= 0 {
			measured = telemetryMeasurement{}
		}
		from := now
		if start, known := measured.start(true); !known {
			from = time.Time{}
		} else if !start.IsZero() {
			from = start
		}
		if interval := telemetryIntervalPolicyLocked(test, from, now); interval.ok && !interval.spanning && interval.segment.Policy != nil {
			e.PolicySegment = interval.segment.Sequence
			e.PolicyHash = interval.segment.Policy.Hash
		}
	}
	e.CounterReadings = map[string]TelemetryCollectionCounter{}
	for name, reading := range telemetryCounterReadings(batch) {
		c := TelemetryCollectionCounter{}
		switch {
		case reading.absent:
			c.Unavailable = "absent"
			if reading.processWide {
				c.Unavailable = "process_wide"
			}
		case reading.value < 0:
			c.Unavailable = "invalid"
		default:
			value := reading.value
			c.Value = &value
		}
		e.CounterReadings[name] = c
	}
	e.Windows = append([]generatorrpc.MetricsCollectionWindow(nil), batch.CollectionWindows...)
	if len(e.Windows) == 0 && len(windows) > 0 && e.Classification != telemetryCollectionClassificationUnknown {
		e.Windows = append([]generatorrpc.MetricsCollectionWindow(nil), windows...)
	}
	if len(e.Windows) > generatorrpc.MaxMetricsCollectionWindows {
		e.Windows = e.Windows[:generatorrpc.MaxMetricsCollectionWindows]
		e.Complete = false
	}
	l := telemetrySourceLedgerLocked(test)
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.Classification == telemetryCollectionClassificationQualifiedSourceWindows && len(windows) > 0 && batch.WindowMs <= 0 {
		offsetDur := time.Duration(math.Round(offsetMs * float64(time.Microsecond) * 1000))
		if l.coverage == nil {
			l.coverage = map[string]*coverageSpans{}
		}
		spans := l.coverage[target]
		if spans == nil {
			spans = &coverageSpans{}
			l.coverage[target] = spans
		}
		for _, w := range windows {
			spans.add(w.Start.Add(-offsetDur).UnixMilli(), w.End.Add(-offsetDur).UnixMilli())
		}
	}
	entries := len(e.Windows)
	for _, c := range l.collections {
		entries += len(c.Windows)
	}
	for len(l.collections) > 0 && (len(l.collections) >= maxRetainedTelemetryCollections || entries > maxRetainedTelemetryWindowEntries) {
		entries -= len(l.collections[0].Windows)
		copy(l.collections, l.collections[1:])
		l.collections = l.collections[:len(l.collections)-1]
		l.collectionsTruncated = true
	}
	l.collections = append(l.collections, e)
}

func cloneTelemetryCollections(in []TelemetryCollectionEvidence) []TelemetryCollectionEvidence {
	if in == nil {
		return nil
	}
	out := make([]TelemetryCollectionEvidence, len(in))
	for i, c := range in {
		c.Windows = append([]generatorrpc.MetricsCollectionWindow(nil), c.Windows...)
		if c.UncertaintyMs != nil {
			u := *c.UncertaintyMs
			c.UncertaintyMs = &u
		}
		if c.ControllerStart != nil {
			s := *c.ControllerStart
			c.ControllerStart = &s
		}
		if c.ControllerEnd != nil {
			e := *c.ControllerEnd
			c.ControllerEnd = &e
		}
		if c.CounterReadings != nil {
			counters := make(map[string]TelemetryCollectionCounter, len(c.CounterReadings))
			for name, r := range c.CounterReadings {
				if r.Value != nil {
					v := *r.Value
					r.Value = &v
				}
				counters[name] = r
			}
			c.CounterReadings = counters
		}
		out[i] = c
	}
	return out
}

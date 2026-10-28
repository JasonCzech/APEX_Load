package api

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"
)

// Render population accounting (TEL-A). Every live point a controller
// publishes to dashboard viewers carries a publication identity: the
// controller's publication epoch for the run, a sequence, whether it carries
// a collection no earlier publication carried (eligible), and the oldest and
// newest collection times it includes. A dashboard tab reports the
// publications it received by identity: rendered (with its receive-to-render
// interval), superseded (a newer publication was rendered before it) or not
// qualified (a hidden tab, a stalled render). The run's reference client is
// the first tab to report a visible render of an identified publication; every
// eligible publication in the range that client reported is then accounted
// as rendered, superseded, unqualified (with its reason) or unreported (never
// reported by the reference client: not delivered, coalesced before its
// receipt, or its report lost). These are delivery outcomes, separate from
// accepted-record loss.

const (
	// telemetryRenderPopulationName declares what the accounting measures:
	// rendered source/cohort publications, not individual actions.
	telemetryRenderPopulationName = "rendered_publications"
	// telemetryRenderLedgerSize bounds the publications the ledger holds. The
	// reference client's range is accounted as it grows, so eviction loses no
	// classification, only the ability to place a later report of an evicted
	// publication (counted as unknown).
	telemetryRenderLedgerSize = 4096
	telemetryRenderRendered   = "rendered"
	telemetryRenderSuperseded = "superseded"
)

// TelemetryPublication identifies one live publication to dashboard viewers.
type TelemetryPublication struct {
	// Epoch names this controller's publications of the run; sequences restart
	// with a new epoch (another controller, or a run reloaded after restart).
	Epoch    string `json:"epoch"`
	Sequence uint64 `json:"sequence"`
	// Eligible is false for a publication that carries no collection an
	// earlier one did not (a lifecycle-only or replayed point): it is not
	// counted in the render population.
	Eligible bool `json:"eligible"`
	// OldestObservationStartAt bounds the earliest MetricsCollectionWindow.Start
	// included in the publication, projected onto the controller clock via its
	// generator's qualified clockCalibration (start - offset - uncertainty).
	OldestObservationStartAt *time.Time `json:"oldest_observation_start_at,omitempty"`
	// OldestCollectedAt and NewestCollectedAt bound the collection times of
	// the windows it includes, each on its generator's clock (see
	// sample_age_quality); a collection covers the window that ends then.
	OldestCollectedAt *time.Time `json:"oldest_collected_at,omitempty"`
	NewestCollectedAt *time.Time `json:"newest_collected_at,omitempty"`
}

// TelemetryRenderPopulation accounts a run's eligible publications for its
// reference client.
type TelemetryRenderPopulation struct {
	Population string `json:"population"`
	Epoch      string `json:"epoch,omitempty"`
	// PublishedEligible counts every eligible publication of the epoch,
	// whether or not a reference client was viewing.
	PublishedEligible int64 `json:"published_eligible"`
	// ReferenceClient is an opaque identifier of the tab whose reports are
	// accounted (its viewer and tab), with the range of sequences it
	// reported; empty until one reported a visible render.
	ReferenceClient string `json:"reference_client,omitempty"`
	FirstSequence   uint64 `json:"first_sequence,omitempty"`
	LastSequence    uint64 `json:"last_sequence,omitempty"`
	// Eligible is the denominator: eligible publications in that range. Each
	// is rendered, superseded, unqualified (by reason) or unreported.
	Eligible    int64            `json:"eligible"`
	Rendered    int64            `json:"rendered"`
	Superseded  int64            `json:"superseded"`
	Unqualified map[string]int64 `json:"unqualified,omitempty"`
	Unreported  int64            `json:"unreported"`
	// Ineligible counts the reference client's reports of publications that
	// carried no new collection; Unknown its reports this ledger cannot place
	// (another epoch, or a sequence it never or no longer holds).
	Ineligible int64 `json:"ineligible,omitempty"`
	Unknown    int64 `json:"unknown,omitempty"`
	// SampledFrom is when this accounting began: the run's start, or when
	// this controller adopted the run (an adoption starts a new epoch, and
	// the earlier ownership's publications are not counted).
	SampledFrom *time.Time `json:"sampled_from,omitempty"`
	// PriorEpochs preserves durable render population provenance recovered
	// from earlier controller epochs across restart or HA failover without
	// inferring exactness for unrecorded windows (TEL-A1).
	PriorEpochs []TelemetryRenderPopulation `json:"prior_epochs,omitempty"`
}

// telemetryPublicationTiming retains the controller-clock bounds of one
// published sequence so a later client render report can compute the
// end-to-end observation-to-render age.
type telemetryPublicationTiming struct {
	publishedAt                 time.Time
	oldestObservationStartAt    time.Time
	newestCollectedControllerAt time.Time
	clockQualified              bool
}

// clientRenderPublication names the publication a client sample is about.
type clientRenderPublication struct {
	Epoch    string `json:"epoch"`
	Sequence uint64 `json:"sequence"`
}

// valid reports whether a client named a publication well formed.
func (p clientRenderPublication) valid() bool {
	return p.Sequence > 0 && p.Epoch != "" && len(p.Epoch) <= 64
}

// clientRenderOutcome is the outcome a client reported for one publication.
type clientRenderOutcome struct {
	publication clientRenderPublication
	outcome     string
}

// telemetryRenderLedger is the recorder's publication ledger; the recorder's
// lock guards it. It holds the newest publications' eligibility and reported
// outcomes, and keeps the reference client's range accounted as it grows, so
// a publication's classification is final before it is evicted.
type telemetryRenderLedger struct {
	epoch     string
	sequence  uint64
	eligible  map[uint64]bool
	timings   map[uint64]telemetryPublicationTiming
	published int64
	// evictedThrough is the newest sequence no longer held.
	evictedThrough uint64
	// reference is the reference client's viewer and tab; referenceID its
	// public, opaque identifier.
	reference, referenceID string
	first, last            uint64
	outcomes               map[uint64]string
	counts                 TelemetryRenderPopulation
	// pendingEligible counts eligible publications evicted past the range's
	// last sequence while it was open: unreported once the range passes them.
	pendingEligible int64
	unknown         int64
	priorEpochs     []TelemetryRenderPopulation
}

// nextPublication assigns the next live publication of the run its identity.
func (r *telemetryStageRecorder) nextPublication(eligible bool) (string, uint64) {
	return r.nextPublicationWithTiming(eligible, telemetryPublicationTiming{})
}

func (r *telemetryStageRecorder) nextPublicationWithTiming(eligible bool, timing telemetryPublicationTiming) (string, uint64) {
	if r == nil {
		return "", 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ledger := &r.render
	if ledger.epoch == "" {
		ledger.epoch, ledger.eligible, ledger.outcomes, ledger.timings = rand.Text(), map[uint64]bool{}, map[uint64]string{}, map[uint64]telemetryPublicationTiming{}
	}
	if ledger.timings == nil {
		ledger.timings = map[uint64]telemetryPublicationTiming{}
	}
	ledger.sequence++
	ledger.eligible[ledger.sequence] = eligible
	ledger.timings[ledger.sequence] = timing
	if eligible {
		ledger.published++
	}
	if ledger.sequence > telemetryRenderLedgerSize {
		evicted := ledger.sequence - telemetryRenderLedgerSize
		if ledger.first > 0 && evicted > ledger.last && ledger.eligible[evicted] {
			ledger.pendingEligible++
		}
		delete(ledger.eligible, evicted)
		delete(ledger.outcomes, evicted)
		delete(ledger.timings, evicted)
		delete(r.presentationProofs, presentationProofKey(ledger.epoch, evicted))
		ledger.evictedThrough = evicted
	}
	return ledger.epoch, ledger.sequence
}

func (r *telemetryStageRecorder) publicationTiming(pub clientRenderPublication) (telemetryPublicationTiming, bool) {
	if r == nil {
		return telemetryPublicationTiming{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.render.epoch == "" || pub.Epoch != r.render.epoch || !r.render.eligible[pub.Sequence] {
		return telemetryPublicationTiming{}, false
	}
	timing, ok := r.render.timings[pub.Sequence]
	return timing, ok
}

func (l *telemetryRenderLedger) held(sequence uint64) bool {
	_, ok := l.eligible[sequence]
	return ok
}

// classify counts an eligible publication of the range under its outcome.
func (l *telemetryRenderLedger) classify(outcome string, reported bool, delta int64) {
	switch {
	case !reported:
		l.counts.Unreported += delta
	case outcome == telemetryRenderRendered:
		l.counts.Rendered += delta
	case outcome == telemetryRenderSuperseded:
		l.counts.Superseded += delta
	default:
		if l.counts.Unqualified == nil {
			l.counts.Unqualified = map[string]int64{}
		}
		l.counts.Unqualified[outcome] += delta
	}
}

// enter adds a held publication to the range's accounting.
func (l *telemetryRenderLedger) enter(sequence uint64) {
	outcome, reported := l.outcomes[sequence]
	if !l.eligible[sequence] {
		if reported {
			l.counts.Ineligible++
		}
		return
	}
	l.counts.Eligible++
	l.classify(outcome, reported, 1)
}

// renderOutcomes records the outcomes one client report gave; key is its
// viewer and tab. Only the reference client is accounted: the first whose
// report includes a visible render of a held publication of the current
// epoch becomes it, with every outcome of that report. A publication keeps
// the first outcome reported for it, and one named twice in a report counts
// as rendered when either copy rendered. It reports which outcomes the
// ledger recorded, by their index.
func (r *telemetryStageRecorder) renderOutcomes(key string, outcomes []clientRenderOutcome) []bool {
	recorded := make([]bool, len(outcomes))
	if r == nil || len(outcomes) == 0 {
		return recorded
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ledger := &r.render
	if ledger.reference == "" && ledger.epoch != "" {
		for _, outcome := range outcomes {
			if outcome.outcome == telemetryRenderRendered && outcome.publication.Epoch == ledger.epoch && ledger.held(outcome.publication.Sequence) {
				sum := sha256.Sum256([]byte(key))
				ledger.reference, ledger.referenceID = key, hex.EncodeToString(sum[:8])
				break
			}
		}
	}
	if ledger.reference == "" || key != ledger.reference {
		return recorded
	}
	order := make([]int, len(outcomes))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(renderOutcomeRank(outcomes[a].outcome), renderOutcomeRank(outcomes[b].outcome))
	})
	for _, index := range order {
		outcome := outcomes[index]
		sequence := outcome.publication.Sequence
		if outcome.publication.Epoch != ledger.epoch || !ledger.held(sequence) {
			ledger.unknown++
			continue
		}
		if _, reported := ledger.outcomes[sequence]; reported {
			continue
		}
		ledger.outcomes[sequence] = outcome.outcome
		recorded[index] = true
		switch {
		case ledger.first == 0:
			ledger.first, ledger.last = sequence, sequence
			ledger.enter(sequence)
		case sequence < ledger.first:
			// Every sequence from this one to the old first is held, as it is.
			for next := sequence; next < ledger.first; next++ {
				ledger.enter(next)
			}
			ledger.first = sequence
		case sequence > ledger.last:
			ledger.counts.Eligible += ledger.pendingEligible
			ledger.counts.Unreported += ledger.pendingEligible
			ledger.pendingEligible = 0
			for next := max(ledger.last, ledger.evictedThrough) + 1; next <= sequence; next++ {
				ledger.enter(next)
			}
			ledger.last = sequence
		case ledger.eligible[sequence]:
			// It was counted as unreported when the range passed it.
			ledger.classify("", false, -1)
			ledger.classify(outcome.outcome, true, 1)
		default:
			ledger.counts.Ineligible++
		}
	}
	return recorded
}

// renderOutcomeRank orders a report's rendered outcomes first.
func renderOutcomeRank(outcome string) int {
	if outcome == telemetryRenderRendered {
		return 0
	}
	return 1
}

// renderPopulation is the run's render accounting so far; nil before its
// first publication.
func (r *telemetryStageRecorder) renderPopulation() *TelemetryRenderPopulation {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.renderPopulationLocked()
}

func (r *telemetryStageRecorder) renderPopulationLocked() *TelemetryRenderPopulation {
	ledger := &r.render
	if ledger.epoch == "" {
		if len(ledger.priorEpochs) == 0 {
			return nil
		}
		latest := ledger.priorEpochs[len(ledger.priorEpochs)-1].clone()
		if len(ledger.priorEpochs) > 1 {
			latest.PriorEpochs = make([]TelemetryRenderPopulation, 0, len(ledger.priorEpochs)-1)
			for i := range ledger.priorEpochs[:len(ledger.priorEpochs)-1] {
				latest.PriorEpochs = append(latest.PriorEpochs, *ledger.priorEpochs[i].clone())
			}
		}
		return latest
	}
	out := ledger.counts.clone()
	out.Population, out.Epoch, out.PublishedEligible, out.ReferenceClient, out.Unknown = telemetryRenderPopulationName, ledger.epoch, ledger.published, ledger.referenceID, ledger.unknown
	out.FirstSequence, out.LastSequence = ledger.first, ledger.last
	since := r.since
	out.SampledFrom = &since
	if len(ledger.priorEpochs) > 0 {
		out.PriorEpochs = make([]TelemetryRenderPopulation, 0, len(ledger.priorEpochs))
		for i := range ledger.priorEpochs {
			out.PriorEpochs = append(out.PriorEpochs, *ledger.priorEpochs[i].clone())
		}
	}
	return out
}

func (p *TelemetryRenderPopulation) clone() *TelemetryRenderPopulation {
	if p == nil {
		return nil
	}
	out := *p
	if p.Unqualified != nil {
		out.Unqualified = make(map[string]int64, len(p.Unqualified))
		for reason, count := range p.Unqualified {
			out.Unqualified[reason] = count
		}
	}
	if p.SampledFrom != nil {
		since := *p.SampledFrom
		out.SampledFrom = &since
	}
	if len(p.PriorEpochs) > 0 {
		out.PriorEpochs = make([]TelemetryRenderPopulation, 0, len(p.PriorEpochs))
		for i := range p.PriorEpochs {
			out.PriorEpochs = append(out.PriorEpochs, *p.PriorEpochs[i].clone())
		}
	}
	return &out
}

func (p *TelemetryPublication) clone() *TelemetryPublication {
	if p == nil {
		return nil
	}
	out := *p
	if p.OldestObservationStartAt != nil {
		at := *p.OldestObservationStartAt
		out.OldestObservationStartAt = &at
	}
	if p.OldestCollectedAt != nil {
		at := *p.OldestCollectedAt
		out.OldestCollectedAt = &at
	}
	if p.NewestCollectedAt != nil {
		at := *p.NewestCollectedAt
		out.NewestCollectedAt = &at
	}
	return &out
}

// assignTelemetryPublicationLocked gives an installed live publication its
// identity; tr.mu is held. A distributed point's install guard decides whether
// it carries a collection no earlier point carried. A local point is a new
// window unless it repeats the previous point's collection time.
func assignTelemetryPublicationLocked(test *ManagedTest, live *LiveMetrics) {
	if test == nil || live == nil || live.Telemetry == nil {
		return
	}
	telemetry := live.Telemetry
	eligible := !telemetry.publicationRepeated
	distributed := len(test.remoteClientsByTarget) > 0 || test.telemetrySources != nil
	if !distributed && test.LiveMetrics != nil && test.LiveMetrics.Telemetry != nil && !telemetry.CollectedAt.IsZero() && test.LiveMetrics.Telemetry.CollectedAt.Equal(telemetry.CollectedAt) {
		eligible = false
	}
	var timing telemetryPublicationTiming
	timing.publishedAt = telemetry.PublishedAt
	if timing.publishedAt.IsZero() {
		timing.publishedAt = telemetry.ControllerReceivedAt
	}
	if timing.publishedAt.IsZero() && !live.Timestamp.IsZero() {
		timing.publishedAt = live.Timestamp
	}
	if timing.publishedAt.IsZero() {
		timing.publishedAt = time.Now().UTC()
	}
	var obsStartPtr *time.Time
	if !telemetry.CollectedAt.IsZero() && !telemetry.collectedAtAssumed && !telemetry.SampleAgeUnavailable {
		qualified := (!distributed && (telemetry.SampleAgeQuality == nil || telemetry.SampleAgeQuality.Qualified)) ||
			(distributed && telemetry.SampleAgeQuality != nil && telemetry.SampleAgeQuality.Qualified)
		if qualified {
			obsStart := telemetry.oldestObservationStartAt
			if obsStart.IsZero() && !distributed {
				obsStart = telemetry.CollectedAt
			}
			newestCtrl := telemetry.newestCollectedControllerAt
			if newestCtrl.IsZero() {
				if ! telemetry.newestCollectedAt.IsZero() && !distributed {
					newestCtrl = telemetry.newestCollectedAt
				} else if !distributed {
					newestCtrl = telemetry.CollectedAt
				} else if !obsStart.IsZero() {
					newestCtrl = obsStart
				}
			}
			if !obsStart.IsZero() {
				utcStart := obsStart.UTC()
				obsStartPtr = &utcStart
				timing.oldestObservationStartAt = utcStart
				timing.newestCollectedControllerAt = newestCtrl.UTC()
				timing.clockQualified = true
			}
		}
	}
	epoch, sequence := stageRecorderLocked(test).nextPublicationWithTiming(eligible, timing)
	publication := &TelemetryPublication{Epoch: epoch, Sequence: sequence, Eligible: eligible, OldestObservationStartAt: obsStartPtr}
	// Without a reported collection time there are no bounds to state.
	if !telemetry.CollectedAt.IsZero() && !telemetry.collectedAtAssumed && !telemetry.SampleAgeUnavailable {
		// Each bound is on its own generator's clock: the newest is chosen on
		// the corrected scale and may read earlier than the oldest's raw time.
		oldest, newest := telemetry.CollectedAt.UTC(), telemetry.CollectedAt.UTC()
		if !telemetry.newestCollectedAt.IsZero() {
			newest = telemetry.newestCollectedAt.UTC()
		}
		publication.OldestCollectedAt, publication.NewestCollectedAt = &oldest, &newest
	}
	telemetry.Publication = publication
}

// distributedPublicationRepeatedLocked reports whether every collection a
// distributed point carries was already published (tr.mu is held). A point
// with a generator whose collection is not identified (an older generator, or
// a batch without telemetry) is never proved repeated.
func distributedPublicationRepeatedLocked(test *ManagedTest, publication distributedMetricsPublication) bool {
	if len(publication.collections) == 0 || len(publication.collections) < max(publication.sources, len(publication.timings)) {
		return false
	}
	for target, collection := range publication.collections {
		if !test.runTotals.publishedWindows[target].sameCollection(collection) {
			return false
		}
	}
	return true
}

package api

import (
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

// Telemetry stage distributions (C5). Each stage a live publication passes
// through keeps a bounded, mergeable t-digest of its own samples for the run:
// generator processing (one generator's own clock), transport (generator to
// controller, cross-host, only once clock-qualified), controller fan-out,
// end-to-end sample age (cross-host, only once clock-qualified) and browser
// render (receive to render in a dashboard tab, opt-in and advisory).
// Percentiles are reported only where a stage was sampled, and stage
// percentiles are never added together: an end-to-end figure is its own
// stage, not a sum of the others.

const (
	telemetryStageGeneratorProcessing = "generator_processing"
	telemetryStageTransport           = "transport"
	telemetryStageControllerFanout    = "controller_fanout"
	telemetryStageSampleAge           = "sample_age"
	telemetryStageBrowserRender       = "browser_render"
	telemetryStageEndToEnd            = "end_to_end"

	telemetryDeliverySignalPostCommitPaintOpportunity = "post_commit_paint_opportunity"
	telemetryDeliverySignalVerifiedFramePresentation  = "verified_frame_presentation"

	DeliverySignalPostCommitPaintOpportunity = telemetryDeliverySignalPostCommitPaintOpportunity
	DeliverySignalVerifiedFramePresentation  = telemetryDeliverySignalVerifiedFramePresentation

	telemetryStageCompression = 100
	// telemetryStageMaxMs bounds a plausible stage sample (one hour).
	telemetryStageMaxMs = 3_600_000
	// telemetryStageSequenceSources bounds the per-generator sequence memory.
	telemetryStageSequenceSources = 1024
)

var telemetryStageOrder = []string{telemetryStageGeneratorProcessing, telemetryStageTransport, telemetryStageControllerFanout, telemetryStageSampleAge, telemetryStageBrowserRender, telemetryStageEndToEnd}

// TelemetryStageDistribution is one stage's sampled distribution so far.
type TelemetryStageDistribution struct {
	Stage string `json:"stage"`
	Unit  string `json:"unit"`
	// Availability is "measured" when the stage has samples, else
	// "unavailable"; percentiles exist only for a measured stage.
	Availability string `json:"availability"`
	Samples      int64  `json:"samples"`
	// Excluded counts observations kept out of the distribution, by reason
	// (an unqualified cross-host clock; a render outside a visible tab).
	Excluded map[string]int64 `json:"excluded,omitempty"`
	P50Ms    *float64         `json:"p50_ms,omitempty"`
	P95Ms    *float64         `json:"p95_ms,omitempty"`
	P99Ms    *float64         `json:"p99_ms,omitempty"`
	MaxMs    *float64         `json:"max_ms,omitempty"`
	// NewestObservation* report the separate newest_observation ->
	// first_visible_paint age percentiles for end_to_end alongside the
	// primary oldest_included_observation -> first_visible_paint bounds.
	NewestObservationP50Ms *float64 `json:"newest_observation_p50_ms,omitempty"`
	NewestObservationP95Ms *float64 `json:"newest_observation_p95_ms,omitempty"`
	NewestObservationP99Ms *float64 `json:"newest_observation_p99_ms,omitempty"`
	NewestObservationMaxMs *float64 `json:"newest_observation_max_ms,omitempty"`
	// EligiblePublications is the full eligible publication denominator
	// against which end_to_end exclusion bounds are evaluated.
	EligiblePublications int64 `json:"eligible_publications,omitempty"`
	// DeliverySignal identifies how browser presentation was observed:
	// "post_commit_paint_opportunity" (advisory rAF/timer callback) or
	// "verified_frame_presentation" (authenticated presentation harness proof
	// attached to the publication correlation ID).
	DeliverySignal string `json:"delivery_signal,omitempty"`
	// Advisory marks a stage that never affects run health (browser
	// render: a viewer's tab is not the system under test).
	Advisory bool `json:"advisory,omitempty"`
	// SampledFrom is when this controller began sampling the run's stages.
	// A controller that adopted the run after a failover or restart samples
	// only from then.
	SampledFrom *time.Time `json:"sampled_from,omitempty"`
	// Reason says why an unavailable stage has no evidence:
	// recorder_lost when the controller that finalized the run never
	// sampled it (it restarted or took over after the run).
	Reason string `json:"reason,omitempty"`
}

// telemetryStageReasonRecorderLost marks a finalized run whose stage
// recorder did not survive (it lives in the sampling controller's memory).
const telemetryStageReasonRecorderLost = "recorder_lost"

type telemetryStageDigest struct {
	digest   *engine.TDigest
	max      float64
	excluded map[string]int64
}

type browserClockCalibration struct {
	clientSendMonoMs     float64
	controllerRecvUnixMs float64
	controllerSendUnixMs float64
	offsetMs             float64
	uncertaintyMs        float64
	calibratedAt         time.Time
}

// telemetryStageRecorder keeps a run's stage digests. It is safe for
// concurrent use.
type telemetryStageRecorder struct {
	mu                  sync.Mutex
	since               time.Time
	stages              map[string]*telemetryStageDigest
	endToEndNewest      *telemetryStageDigest
	browserRenderSignal string
	endToEndSignal      string
	browserClocks       map[string]browserClockCalibration
	cborder             []string
	presentationProofs  map[string]string
	pborder             []string
	lastSeen            map[string]generatorBatchPosition
	// render is the run's publication ledger and reference-client render
	// accounting (TEL-A).
	render telemetryRenderLedger
}

// generatorBatchPosition is the newest batch a generator's stages were
// sampled from, so a re-aggregated or retried batch is sampled once.
type generatorBatchPosition struct {
	epoch     string
	sequence  uint64
	timestamp int64
}

// covers reports whether a batch at next was already sampled at p: within one
// epoch, by sequence, or by batch timestamp when both sequences are 0 (an
// older generator, whose sequence is unknown).
func (p generatorBatchPosition) covers(next generatorBatchPosition) bool {
	if p.epoch != next.epoch {
		return false
	}
	if next.sequence > 0 && p.sequence > 0 {
		return next.sequence <= p.sequence
	}
	if next.sequence == 0 && p.sequence == 0 && next.timestamp > 0 && p.timestamp > 0 {
		return next.timestamp <= p.timestamp
	}
	return false
}

func newTelemetryStageRecorder() *telemetryStageRecorder {
	return &telemetryStageRecorder{since: time.Now().UTC(), stages: map[string]*telemetryStageDigest{}, lastSeen: map[string]generatorBatchPosition{}}
}

// adopted records that this controller (re)adopted the run at: a recorder it
// kept from an earlier ownership missed the gap and the other controller's
// time, so its distributions count only as sampled from the adoption.
func (r *telemetryStageRecorder) adopted(at time.Time) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var priors []TelemetryRenderPopulation
	if len(r.render.priorEpochs) > 0 {
		priors = append(priors, r.render.priorEpochs...)
	}
	if r.render.epoch != "" {
		if pop := r.renderPopulationLocked(); pop != nil {
			pop.PriorEpochs = nil
			if pop.PublishedEligible > pop.Eligible {
				pop.Unreported += pop.PublishedEligible - pop.Eligible
			}
			priors = append(priors, *pop)
		}
	}
	if r.since.Before(at) {
		r.since = at.UTC()
	}
	// Its publications start a new epoch: the reference client and the
	// accounting of the earlier ownership do not carry over into the active
	// epoch, while prior epochs remain recorded as durable provenance (TEL-A1).
	r.render = telemetryRenderLedger{priorEpochs: priors}
}

func presentationProofKey(epoch string, sequence uint64) string {
	return epoch + "\x00" + strconv.FormatUint(sequence, 10)
}

// RegisterPresentationProof registers an authenticated presentation harness
// proof for a publication correlation ID (epoch + sequence).
func (r *telemetryStageRecorder) RegisterPresentationProof(epoch string, sequence uint64, proof string) {
	if r == nil || epoch == "" || sequence == 0 || proof == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.presentationProofs == nil {
		r.presentationProofs = map[string]string{}
	}
	key := presentationProofKey(epoch, sequence)
	if _, exists := r.presentationProofs[key]; !exists {
		r.pborder = append(r.pborder, key)
		if len(r.pborder) > telemetryRenderLedgerSize {
			delete(r.presentationProofs, r.pborder[0])
			r.pborder = r.pborder[1:]
		}
	}
	r.presentationProofs[key] = proof
}

func (r *telemetryStageRecorder) resolveSampleDeliverySignalLocked(sample clientRenderSample) string {
	if sample.DeliverySignal != telemetryDeliverySignalVerifiedFramePresentation || sample.Publication == nil || !sample.Publication.valid() || sample.PresentationProof == "" {
		return telemetryDeliverySignalPostCommitPaintOpportunity
	}
	expected, ok := r.presentationProofs[presentationProofKey(sample.Publication.Epoch, sample.Publication.Sequence)]
	if !ok || expected != sample.PresentationProof {
		return telemetryDeliverySignalPostCommitPaintOpportunity
	}
	return telemetryDeliverySignalVerifiedFramePresentation
}

func mergeDeliverySignal(current, sampleSignal string) string {
	normalized := telemetryDeliverySignalPostCommitPaintOpportunity
	if sampleSignal == telemetryDeliverySignalVerifiedFramePresentation {
		normalized = telemetryDeliverySignalVerifiedFramePresentation
	}
	if current == "" {
		return normalized
	}
	if current == telemetryDeliverySignalVerifiedFramePresentation && normalized == telemetryDeliverySignalVerifiedFramePresentation {
		return telemetryDeliverySignalVerifiedFramePresentation
	}
	return telemetryDeliverySignalPostCommitPaintOpportunity
}

const clientRenderCalibrationTTL = 5 * time.Minute

func (r *telemetryStageRecorder) recordBrowserCalibration(clientKey string, clientSendMonoMs float64, clientRecvMonoMs *float64, controllerRecv, controllerSend time.Time) [3]float64 {
	recvMs := float64(controllerRecv.UTC().UnixNano()) / 1e6
	sendMs := float64(controllerSend.UTC().UnixNano()) / 1e6
	if sendMs < recvMs {
		sendMs = recvMs
	}
	echo := [3]float64{clientSendMonoMs, math.Round(recvMs*1000) / 1000, math.Round(sendMs*1000) / 1000}
	if r == nil {
		return echo
	}
	offsetMs := recvMs - clientSendMonoMs
	uncertaintyMs := max(0, sendMs-recvMs)
	if clientRecvMonoMs != nil && !math.IsNaN(*clientRecvMonoMs) && !math.IsInf(*clientRecvMonoMs, 0) && *clientRecvMonoMs >= clientSendMonoMs {
		rttMs := max(0, (*clientRecvMonoMs-clientSendMonoMs)-(sendMs-recvMs))
		uncertaintyMs = rttMs/2 + max(0, sendMs-recvMs)/2
		offsetMs = (recvMs+sendMs)/2 - (clientSendMonoMs+*clientRecvMonoMs)/2
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.browserClocks == nil {
		r.browserClocks = map[string]browserClockCalibration{}
	}
	if _, exists := r.browserClocks[clientKey]; !exists {
		r.cborder = append(r.cborder, clientKey)
		if len(r.cborder) > clientRenderMaxReporters {
			delete(r.browserClocks, r.cborder[0])
			r.cborder = r.cborder[1:]
		}
	}
	r.browserClocks[clientKey] = browserClockCalibration{
		clientSendMonoMs:     clientSendMonoMs,
		controllerRecvUnixMs: echo[1],
		controllerSendUnixMs: echo[2],
		offsetMs:             offsetMs,
		uncertaintyMs:        uncertaintyMs,
		calibratedAt:         controllerRecv.UTC(),
	}
	return echo
}

func (r *telemetryStageRecorder) refineBrowserCalibration(clientKey string, clientSendMonoMs, clientRecvMonoMs, controllerRecvUnixMs, controllerSendUnixMs float64) bool {
	if r == nil || math.IsNaN(clientRecvMonoMs) || math.IsInf(clientRecvMonoMs, 0) || clientRecvMonoMs < clientSendMonoMs {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cal, ok := r.browserClocks[clientKey]
	if !ok {
		return false
	}
	if math.Abs(cal.clientSendMonoMs-clientSendMonoMs) > 0.001 ||
		math.Abs(cal.controllerRecvUnixMs-controllerRecvUnixMs) > 0.001 ||
		math.Abs(cal.controllerSendUnixMs-controllerSendUnixMs) > 0.001 {
		return false
	}
	rttMs := max(0, (clientRecvMonoMs-clientSendMonoMs)-(controllerSendUnixMs-controllerRecvUnixMs))
	cal.uncertaintyMs = rttMs/2 + max(0, controllerSendUnixMs-controllerRecvUnixMs)/2
	cal.offsetMs = (controllerRecvUnixMs+controllerSendUnixMs)/2 - (clientSendMonoMs+clientRecvMonoMs)/2
	r.browserClocks[clientKey] = cal
	return true
}

func (r *telemetryStageRecorder) observeBrowserRenderSample(sample clientRenderSample) {
	if r == nil {
		return
	}
	r.observe(telemetryStageBrowserRender, sample.ReceiveToRenderMs)
	r.mu.Lock()
	signal := r.resolveSampleDeliverySignalLocked(sample)
	r.browserRenderSignal = mergeDeliverySignal(r.browserRenderSignal, signal)
	r.mu.Unlock()
}

func (r *telemetryStageRecorder) observeReferencePublicationEndToEnd(clientKey string, sample clientRenderSample, now time.Time) {
	if r == nil || sample.Publication == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.render.epoch == "" || sample.Publication.Epoch != r.render.epoch || !r.render.eligible[sample.Publication.Sequence] {
		return
	}
	timing, hasTiming := r.render.timings[sample.Publication.Sequence]
	cal, hasCal := r.browserClocks[clientKey]
	if hasCal && now.Sub(cal.calibratedAt) > clientRenderCalibrationTTL {
		hasCal = false
	}
	if !hasTiming || !timing.clockQualified || !hasCal {
		r.excludeLocked(r.stageLocked(telemetryStageEndToEnd), "clock_unqualified")
		return
	}
	pubMs := float64(timing.publishedAt.UnixNano()) / 1e6
	reportRecvMs := float64(now.UTC().UnixNano()) / 1e6
	var rxUpperMs float64
	switch {
	case sample.ReceivedMonoMs != nil:
		rxUpperMs = *sample.ReceivedMonoMs + cal.offsetMs + cal.uncertaintyMs
		if rxUpperMs < pubMs {
			rxUpperMs = pubMs
		}
	case sample.RenderedMonoMs != nil:
		rxMono := *sample.RenderedMonoMs - sample.ReceiveToRenderMs
		rxUpperMs = max(pubMs, rxMono+cal.offsetMs+cal.uncertaintyMs)
	default:
		rxUpperMs = max(pubMs+cal.uncertaintyMs, reportRecvMs-sample.ReceiveToRenderMs)
	}
	oldestStartMs := float64(timing.oldestObservationStartAt.UnixNano()) / 1e6
	newestCtrlMs := float64(timing.newestCollectedControllerAt.UnixNano()) / 1e6
	if newestCtrlMs <= 0 {
		newestCtrlMs = oldestStartMs
	}
	e2eOldestMs := max(0, rxUpperMs-oldestStartMs) + sample.ReceiveToRenderMs
	e2eNewestMs := max(0, rxUpperMs-newestCtrlMs) + sample.ReceiveToRenderMs
	current := r.stageLocked(telemetryStageEndToEnd)
	if !validTelemetryStageMs(e2eOldestMs) || !validTelemetryStageMs(e2eNewestMs) {
		r.excludeLocked(current, "invalid")
		return
	}
	current.digest.Add(e2eOldestMs)
	current.max = max(current.max, e2eOldestMs)
	if r.endToEndNewest == nil {
		r.endToEndNewest = &telemetryStageDigest{digest: engine.NewTDigest(telemetryStageCompression)}
	}
	r.endToEndNewest.digest.Add(e2eNewestMs)
	r.endToEndNewest.max = max(r.endToEndNewest.max, e2eNewestMs)
	r.endToEndSignal = mergeDeliverySignal(r.endToEndSignal, r.resolveSampleDeliverySignalLocked(sample))
}

func (r *telemetryStageRecorder) stageLocked(stage string) *telemetryStageDigest {
	current := r.stages[stage]
	if current == nil {
		current = &telemetryStageDigest{digest: engine.NewTDigest(telemetryStageCompression)}
		r.stages[stage] = current
	}
	return current
}

func validTelemetryStageMs(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= telemetryStageMaxMs
}

// observe adds one sample to a stage; an implausible value is excluded as
// "invalid".
func (r *telemetryStageRecorder) observe(stage string, valueMs float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.stageLocked(stage)
	if !validTelemetryStageMs(valueMs) {
		r.excludeLocked(current, "invalid")
		return
	}
	current.digest.Add(valueMs)
	current.max = max(current.max, valueMs)
}

// exclude counts an observation kept out of a stage's distribution.
func (r *telemetryStageRecorder) exclude(stage, reason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.excludeLocked(r.stageLocked(stage), reason)
}

func (r *telemetryStageRecorder) excludeLocked(current *telemetryStageDigest, reason string) {
	if current.excluded == nil {
		current.excluded = map[string]int64{}
	}
	current.excluded[reason]++
}

// observeGeneratorBatch samples each accepted collection once: its own
// processing lag, and its transport lag when the generator's clock was
// qualified at receipt. Without a collection identity, legacy peers retain
// the batch-position fallback; known lifecycle/subscription replays do not
// turn the same collection into another timing observation.
func (r *telemetryStageRecorder) observeGeneratorBatch(target string, batch generatorrpc.MetricsBatch) {
	if r == nil || batch.Telemetry == nil {
		return
	}
	position := generatorBatchPosition{epoch: batch.SourceEpoch, sequence: batch.BatchSequence, timestamp: batch.TimestampUnixMs}
	r.mu.Lock()
	if last, ok := r.lastSeen[target]; ok && last.covers(position) {
		r.mu.Unlock()
		return
	}
	if _, ok := r.lastSeen[target]; !ok && len(r.lastSeen) >= telemetryStageSequenceSources {
		r.mu.Unlock()
		return
	}
	r.lastSeen[target] = position
	r.mu.Unlock()
	telemetry := batch.Telemetry
	if telemetry.CollectionRepeated {
		return
	}
	processing := telemetry.GeneratorProcessingLagMs
	if processing <= 0 && telemetry.CollectedAtUnixMs > 0 && telemetry.PublishedAtUnixMs > 0 {
		processing = float64(telemetry.PublishedAtUnixMs - telemetry.CollectedAtUnixMs)
	}
	if processing > 0 {
		r.observe(telemetryStageGeneratorProcessing, processing)
	}
	switch {
	case telemetry.TransportLagMs <= 0 && telemetry.TransportLagQuality == nil:
	case telemetry.TransportLagQuality != nil && telemetry.TransportLagQuality.Qualified:
		r.observe(telemetryStageTransport, telemetry.TransportLagMs)
	default:
		r.exclude(telemetryStageTransport, "clock_unqualified")
	}
}

// snapshot reports every stage in a fixed order.
func (r *telemetryStageRecorder) snapshot() []TelemetryStageDistribution {
	out := make([]TelemetryStageDistribution, 0, len(telemetryStageOrder))
	if r == nil {
		return out
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, stage := range telemetryStageOrder {
		since := r.since
		distribution := TelemetryStageDistribution{Stage: stage, Unit: "ms", Availability: "unavailable", Advisory: stage == telemetryStageBrowserRender, SampledFrom: &since}
		if current := r.stages[stage]; current != nil {
			distribution.Samples = current.digest.Count()
			if len(current.excluded) > 0 {
				distribution.Excluded = make(map[string]int64, len(current.excluded))
				for reason, count := range current.excluded {
					distribution.Excluded[reason] = count
				}
			}
			if distribution.Samples > 0 {
				quantile := func(q float64) *float64 { value := math.Round(current.digest.Quantile(q)*1000) / 1000; return &value }
				maxValue := current.max
				distribution.Availability, distribution.P50Ms, distribution.P95Ms, distribution.P99Ms, distribution.MaxMs = "measured", quantile(0.50), quantile(0.95), quantile(0.99), &maxValue
			}
		}
		switch stage {
		case telemetryStageBrowserRender:
			if distribution.Samples > 0 {
				signal := r.browserRenderSignal
				if signal == "" {
					signal = telemetryDeliverySignalPostCommitPaintOpportunity
				}
				distribution.DeliverySignal = signal
				distribution.Advisory = signal != telemetryDeliverySignalVerifiedFramePresentation
			}
		case telemetryStageEndToEnd:
			denom := max(r.render.published, r.render.counts.Eligible)
			for _, prior := range r.render.priorEpochs {
				denom += max(prior.PublishedEligible, prior.Eligible)
			}
			hasRenderActivity := distribution.Samples > 0 || len(distribution.Excluded) > 0 || r.render.reference != ""
			if hasRenderActivity && denom > 0 {
				distribution.EligiblePublications = denom
				if distribution.Excluded == nil {
					distribution.Excluded = map[string]int64{}
				}
				if r.render.counts.Superseded > 0 {
					distribution.Excluded["superseded"] += r.render.counts.Superseded
				}
				for reason, count := range r.render.counts.Unqualified {
					if count > 0 {
						distribution.Excluded[reason] += count
					}
				}
				for _, prior := range r.render.priorEpochs {
					if prior.Superseded > 0 {
						distribution.Excluded["superseded"] += prior.Superseded
					}
					for reason, count := range prior.Unqualified {
						if count > 0 {
							distribution.Excluded[reason] += count
						}
					}
				}
				var accountedExcluded int64
				for _, count := range distribution.Excluded {
					accountedExcluded += count
				}
				if unreported := denom - distribution.Samples - accountedExcluded; unreported > 0 {
					distribution.Excluded["unreported"] += unreported
				}
				if len(distribution.Excluded) == 0 {
					distribution.Excluded = nil
				}
			}
			if distribution.Samples > 0 {
				signal := r.endToEndSignal
				if signal == "" {
					signal = telemetryDeliverySignalPostCommitPaintOpportunity
				}
				distribution.DeliverySignal = signal
				distribution.Advisory = signal != telemetryDeliverySignalVerifiedFramePresentation
				if r.endToEndNewest != nil && r.endToEndNewest.digest.Count() > 0 {
					newestQuantile := func(q float64) *float64 {
						value := math.Round(r.endToEndNewest.digest.Quantile(q)*1000) / 1000
						return &value
					}
					newestMax := r.endToEndNewest.max
					distribution.NewestObservationP50Ms = newestQuantile(0.50)
					distribution.NewestObservationP95Ms = newestQuantile(0.95)
					distribution.NewestObservationP99Ms = newestQuantile(0.99)
					distribution.NewestObservationMaxMs = &newestMax
				}
			}
		}
		out = append(out, distribution)
	}
	return out
}

// digests returns each sampled stage's digest snapshot, for the archive.
func (r *telemetryStageRecorder) digests() map[string]engine.TDigestSnapshot {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]engine.TDigestSnapshot{}
	for stage, current := range r.stages {
		if current.digest.Count() > 0 {
			out[stage] = current.digest.Snapshot()
		}
	}
	return out
}

// quantile returns a sampled stage's quantile, rounded as snapshot reports
// it.
func (r *telemetryStageRecorder) quantile(stage string, q float64) (float64, bool) {
	if r == nil {
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.stages[stage]
	if current == nil || current.digest.Count() == 0 {
		return 0, false
	}
	return math.Round(current.digest.Quantile(q)*1000) / 1000, true
}

// stageRecorderLocked returns a run's stage recorder, creating it; tr.mu is
// held.
func stageRecorderLocked(test *ManagedTest) *telemetryStageRecorder {
	if test.telemetryStages == nil {
		test.telemetryStages = newTelemetryStageRecorder()
		// The controller that started the run samples it from its start (no
		// stage is observed before the first publication); one that adopted
		// it after a failover samples only from then, which acceptance
		// verdicts treat as partial evidence.
		if test.StartedAt != nil && len(test.continuityFailovers) == 0 && test.telemetryStages.since.After(*test.StartedAt) {
			test.telemetryStages.since = test.StartedAt.UTC()
		}
	}
	return test.telemetryStages
}

// liveStageRecorder returns the recorder of the in-memory run (never a read
// snapshot, whose recorder would be lost) while it is live, with its ID,
// project and start time.
func (tr *TestRunner) liveStageRecorder(ref string) (recorder *telemetryStageRecorder, id, projectID string, started time.Time, live bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	test := tr.tests[tr.resolveRefKeyLocked(ref)]
	if test == nil {
		return nil, "", "", time.Time{}, false
	}
	switch test.Status {
	case "STARTING", "RUNNING", "PAUSED", "STOPPING":
	default:
		return nil, "", "", time.Time{}, false
	}
	if test.StartedAt != nil {
		started = *test.StartedAt
	}
	return stageRecorderLocked(test), test.ID, test.ProjectID, started, true
}

// sampleTelemetryStagesLocked samples one installed live publication's
// controller-side stages; tr.mu is held (the recorder's own lock nests inside
// it), so a snapshot an install guard rejects is never sampled. Fan-out is
// the controller's own; sample age is cross-host for a distributed run and
// enters the distribution only once qualified. A run executed in this
// process samples its own processing lag here (a distributed run samples it
// per generator batch). Browser render, from client reports, sets the
// advisory BrowserRenderLagMs to its visible p95. The distributions
// themselves are not stored on the snapshot: it is also the newest history
// point, and history carries none.
func sampleTelemetryStagesLocked(test *ManagedTest, live *LiveMetrics) {
	if test == nil || live == nil || live.Telemetry == nil {
		return
	}
	distributed := len(test.remoteClientsByTarget) > 0 || test.telemetrySources != nil
	recorder := stageRecorderLocked(test)
	telemetry := live.Telemetry
	if !telemetry.ControllerReceivedAt.IsZero() {
		recorder.observe(telemetryStageControllerFanout, telemetry.ControllerFanoutLagMs)
	}
	if !distributed && telemetry.GeneratorProcessingLagMs > 0 {
		recorder.observe(telemetryStageGeneratorProcessing, telemetry.GeneratorProcessingLagMs)
	}
	switch {
	case telemetry.SampleAgeUnavailable || telemetry.CollectedAt.IsZero():
	case telemetry.SampleAgeQuality == nil && !distributed, telemetry.SampleAgeQuality != nil && telemetry.SampleAgeQuality.Qualified:
		recorder.observe(telemetryStageSampleAge, telemetry.SampleAgeMs)
	default:
		recorder.exclude(telemetryStageSampleAge, "clock_unqualified")
	}
	if p95, ok := recorder.quantile(telemetryStageBrowserRender, 0.95); ok {
		telemetry.BrowserRenderLagMs = p95
	}
}

// attachTelemetryStageDistributions adds the run's distributions so far to a
// public projection of its current snapshot (a clone; tr.mu is held).
func attachTelemetryStageDistributions(metrics *LiveMetrics, test *ManagedTest) {
	if metrics == nil || metrics.Telemetry == nil || test == nil || test.telemetryStages == nil {
		return
	}
	metrics.Telemetry.StageDistributions = test.telemetryStages.snapshot()
	metrics.Telemetry.RenderPopulation = test.telemetryStages.renderPopulation()
}

func cloneTelemetryStageDistributions(in []TelemetryStageDistribution) []TelemetryStageDistribution {
	if in == nil {
		return nil
	}
	out := make([]TelemetryStageDistribution, len(in))
	for i, distribution := range in {
		out[i] = distribution
		if distribution.Excluded != nil {
			out[i].Excluded = make(map[string]int64, len(distribution.Excluded))
			for reason, count := range distribution.Excluded {
				out[i].Excluded[reason] = count
			}
		}
		for _, field := range []**float64{&out[i].P50Ms, &out[i].P95Ms, &out[i].P99Ms, &out[i].MaxMs, &out[i].NewestObservationP50Ms, &out[i].NewestObservationP95Ms, &out[i].NewestObservationP99Ms, &out[i].NewestObservationMaxMs} {
			if *field != nil {
				value := **field
				*field = &value
			}
		}
		if distribution.SampledFrom != nil {
			since := *distribution.SampledFrom
			out[i].SampledFrom = &since
		}
	}
	return out
}

// persistTelemetryStageDistributions records the run's stage distributions
// and their digests in the manifest at finalize. A run that started but whose
// recorder this controller does not hold (a finalize after a restart or
// failover) records every stage as unavailable with recorder_lost, so it is
// never mistaken for a run without stage evidence by design. An adjunct
// refresh (refresh) never records recorder_lost: a manifest without stages
// then predates stage sampling, and the field is written once.
func (tr *TestRunner) persistTelemetryStageDistributions(test *ManagedTest, manifest *RunArchiveManifest, refresh bool) {
	if test == nil || manifest == nil || manifest.TelemetryStageDistributions != nil {
		return
	}
	tr.mu.RLock()
	recorder, started := test.telemetryStages, test.StartedAt != nil
	tr.mu.RUnlock()
	if recorder == nil {
		if !started || refresh {
			return
		}
		lost := make([]TelemetryStageDistribution, 0, len(telemetryStageOrder))
		for _, stage := range telemetryStageOrder {
			lost = append(lost, TelemetryStageDistribution{Stage: stage, Unit: "ms", Availability: "unavailable", Advisory: stage == telemetryStageBrowserRender, Reason: telemetryStageReasonRecorderLost})
		}
		manifest.TelemetryStageDistributions = lost
		return
	}
	manifest.TelemetryStageDistributions = recorder.snapshot()
	manifest.TelemetryStageDigests = recorder.digests()
	manifest.TelemetryRenderPopulation = recorder.renderPopulation()
}

// clientRenderSample is one dashboard tab's measured time from receiving a
// live publication to rendering it.
type clientRenderSample struct {
	ReceiveToRenderMs float64   `json:"receive_to_render_ms"`
	State             string    `json:"state"` // visible, background, disconnected, superseded
	ObservedAt        time.Time `json:"observed_at"`
	// ReceivedMonoMs and RenderedMonoMs are the browser's performance.now()
	// monotonic timestamps when the publication arrived and when its paint
	// callback fired (TEL-A1).
	ReceivedMonoMs *float64 `json:"received_mono_ms,omitempty"`
	RenderedMonoMs *float64 `json:"rendered_mono_ms,omitempty"`
	// DeliverySignal is "post_commit_paint_opportunity" (default rAF/timer
	// callback, advisory) or "verified_frame_presentation" (authenticated
	// presentation harness proof attached to the publication correlation ID).
	DeliverySignal    string `json:"delivery_signal,omitempty"`
	PresentationProof string `json:"presentation_proof,omitempty"`
	// Publication names the live publication the sample is about; a newer
	// tab reports it, an older one does not (TEL-A).
	Publication *clientRenderPublication `json:"publication,omitempty"`
}

const (
	// clientRenderCeilingMs bounds a visible render: a tab that took longer
	// stalled (a debugger, a frozen page) rather than rendered slowly.
	clientRenderCeilingMs = 60_000
	// clientRenderClockSlack allows for a client clock that is not
	// qualified against the controller's.
	clientRenderClockSlack = 5 * time.Minute
)

// admitClientRenderSample decides whether a client sample counts toward the
// run's visible-render distribution, and otherwise why not (the reason the
// stage's Excluded map counts). Only a visible tab's render counts: a
// background tab's throttled timers would inflate it, and a disconnected
// client rendered nothing new.
func admitClientRenderSample(sample clientRenderSample, runStarted, now time.Time) (bool, string) {
	switch sample.State {
	case "visible":
	case "background":
		return false, "background_tab"
	case "disconnected":
		return false, "disconnected"
	case "superseded":
		// Received, but a newer publication was rendered before it.
		return false, "superseded"
	default:
		return false, "unknown_state"
	}
	if !validTelemetryStageMs(sample.ReceiveToRenderMs) {
		return false, "invalid"
	}
	if sample.ReceivedMonoMs != nil && (math.IsNaN(*sample.ReceivedMonoMs) || math.IsInf(*sample.ReceivedMonoMs, 0) || *sample.ReceivedMonoMs < 0) {
		return false, "invalid"
	}
	if sample.RenderedMonoMs != nil && (math.IsNaN(*sample.RenderedMonoMs) || math.IsInf(*sample.RenderedMonoMs, 0) || *sample.RenderedMonoMs < 0) {
		return false, "invalid"
	}
	if sample.ReceivedMonoMs != nil && sample.RenderedMonoMs != nil {
		monoInterval := *sample.RenderedMonoMs - *sample.ReceivedMonoMs
		if monoInterval < 0 || math.Abs(monoInterval-sample.ReceiveToRenderMs) > 1.0 {
			return false, "invalid"
		}
	}
	switch sample.DeliverySignal {
	case "", telemetryDeliverySignalPostCommitPaintOpportunity:
	case telemetryDeliverySignalVerifiedFramePresentation:
		if sample.Publication == nil || !sample.Publication.valid() {
			return false, "invalid"
		}
	default:
		return false, "invalid"
	}
	if sample.ReceiveToRenderMs > clientRenderCeilingMs {
		return false, "stalled_render"
	}
	switch {
	case sample.ObservedAt.IsZero():
		return false, "missing_time"
	case !runStarted.IsZero() && sample.ObservedAt.Before(runStarted.Add(-clientRenderClockSlack)):
		return false, "before_run"
	case sample.ObservedAt.After(now.Add(clientRenderClockSlack)):
		return false, "future_time"
	}
	return true, ""
}

package api

import (
	"math"
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

	telemetryStageCompression = 100
	// telemetryStageMaxMs bounds a plausible stage sample (one hour).
	telemetryStageMaxMs = 3_600_000
	// telemetryStageSequenceSources bounds the per-generator sequence memory.
	telemetryStageSequenceSources = 1024
)

var telemetryStageOrder = []string{telemetryStageGeneratorProcessing, telemetryStageTransport, telemetryStageControllerFanout, telemetryStageSampleAge, telemetryStageBrowserRender}

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

// telemetryStageRecorder keeps a run's stage digests. It is safe for
// concurrent use.
type telemetryStageRecorder struct {
	mu       sync.Mutex
	since    time.Time
	stages   map[string]*telemetryStageDigest
	lastSeen map[string]generatorBatchPosition
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
	if r.since.Before(at) {
		r.since = at.UTC()
	}
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

// observeGeneratorBatch samples a newly received generator batch once: its
// own processing lag, and its transport lag when the generator's clock was
// qualified at receipt.
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
		for _, field := range []**float64{&out[i].P50Ms, &out[i].P95Ms, &out[i].P99Ms, &out[i].MaxMs} {
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
}

// clientRenderSample is one dashboard tab's measured time from receiving a
// live publication to rendering it.
type clientRenderSample struct {
	ReceiveToRenderMs float64   `json:"receive_to_render_ms"`
	State             string    `json:"state"` // visible, background, disconnected
	ObservedAt        time.Time `json:"observed_at"`
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
	default:
		return false, "unknown_state"
	}
	if !validTelemetryStageMs(sample.ReceiveToRenderMs) {
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

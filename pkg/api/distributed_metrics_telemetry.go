package api

import (
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// distributedTelemetryAccumulator merges only timing and receipt/counter facts;
// it never traverses metric payloads. The same rules serve initial aggregation
// and a prepared publication that waited behind another installed revision.
type distributedTelemetryAccumulator struct {
	telemetry                                               *generatorrpc.MetricsTelemetry
	publishedAt                                             time.Time
	hasCollectedAt, hasPublishedAt, hasControllerReceivedAt bool
	dropCounterUnavailable                                  bool
	oldestCollectedMs, newestCollectedMs                    float64
}

func (a *distributedTelemetryAccumulator) add(state string, telemetry *generatorrpc.MetricsTelemetry) {
	if telemetry == nil || telemetry.DroppedResultsUnavailable || telemetry.DroppedResults < 0 {
		a.dropCounterUnavailable = true
	}
	if telemetry == nil {
		return
	}
	if a.telemetry == nil {
		// The aggregate's qualification is judged now: publication
		// re-checks for a step since this instant.
		a.telemetry = &generatorrpc.MetricsTelemetry{ReceivedAt: a.publishedAt.Round(0), ReceivedMonotonic: a.publishedAt.Sub(clockProcessStart)}
	}
	var validDrops bool
	a.telemetry.DroppedResults, validDrops = mergeRetainedDropCounters(a.telemetry.DroppedResults, telemetry.DroppedResults)
	if !validDrops {
		a.dropCounterUnavailable = true
	}
	a.telemetry.SheddedArrivals += telemetry.SheddedArrivals
	a.telemetry.DroppedSubscriberBatches += telemetry.DroppedSubscriberBatches
	a.telemetry.MissingMetricWindows += telemetry.MissingMetricWindows
	if telemetry.ControllerReceivedAtUnixMs > 0 && (!a.hasControllerReceivedAt || telemetry.ControllerReceivedAtUnixMs > a.telemetry.ControllerReceivedAtUnixMs) {
		a.telemetry.ControllerReceivedAtUnixMs = telemetry.ControllerReceivedAtUnixMs
		a.hasControllerReceivedAt = true
	}
	// Finalizing/terminal envelopes can replay the final collection after archive
	// finalization. Only an already installed collection is lifecycle
	// delivery rather than newly delayed metrics. Receipt alone is
	// insufficient: a throttled/rejected first publication still ages.
	// Retain counters and receipt/fan-out timing. An unseen terminal
	// collection still contributes all timing, as does an active
	// generator's retained window (whose freshness still matters).
	if telemetry.CollectionPublished && generatorrpc.IsMetricsCollectionFinal(state) {
		return
	}
	if telemetry.CollectedAtUnixMs > 0 {
		// The oldest sample is the earliest on the controller's
		// clock: a qualified collection time maps to it by its own
		// generator's offset. Its quality is the worst of every
		// contributing generator's, and the qualified ones bound the
		// fleet's age from below.
		collected := float64(telemetry.CollectedAtUnixMs)
		quality, offset := telemetry.CollectedAtQuality, telemetry.CollectedAtOffsetMs
		if wallClockSteppedSince(telemetry.ReceivedAt, telemetry.ReceivedMonotonic, a.publishedAt) {
			// Qualified on receipt, but the controller's clock has
			// stepped since: its age now would include the step.
			quality, offset = &generatorrpc.ClockQuality{Reason: clockReasonStep}, 0
		}
		if quality != nil && quality.Qualified {
			collected -= offset
			if bound := collected + quality.UncertaintyMs; a.telemetry.CollectedAtBoundUnixMs == 0 || bound < a.telemetry.CollectedAtBoundUnixMs {
				a.telemetry.CollectedAtBoundUnixMs = bound
			}
		}
		if !a.hasCollectedAt || collected < a.oldestCollectedMs {
			a.oldestCollectedMs = collected
			a.telemetry.CollectedAtUnixMs, a.telemetry.CollectedAtOffsetMs = telemetry.CollectedAtUnixMs, offset
		}
		// The newest is chosen on the same corrected scale as the oldest.
		if !a.hasCollectedAt || collected > a.newestCollectedMs {
			a.newestCollectedMs = collected
			a.telemetry.NewestCollectedAtUnixMs = telemetry.CollectedAtUnixMs
		}
		a.telemetry.CollectedAtQuality = combineClockQuality(a.telemetry.CollectedAtQuality, quality, 0)
		a.hasCollectedAt = true
	}
	if telemetry.PublishedAtUnixMs > 0 && (!a.hasPublishedAt || telemetry.PublishedAtUnixMs > a.telemetry.PublishedAtUnixMs) {
		a.telemetry.PublishedAtUnixMs = telemetry.PublishedAtUnixMs
		a.hasPublishedAt = true
	}
	if telemetry.SampleAgeMs > a.telemetry.SampleAgeMs {
		a.telemetry.SampleAgeMs = telemetry.SampleAgeMs
	}
	// Processing lag is measured on one generator's own clock; an
	// older generator that does not report it is derived from its
	// own two timestamps, never across generators.
	processingLag := telemetry.GeneratorProcessingLagMs
	if processingLag <= 0 && telemetry.CollectedAtUnixMs > 0 && telemetry.PublishedAtUnixMs > 0 {
		processingLag = float64(telemetry.PublishedAtUnixMs - telemetry.CollectedAtUnixMs)
	}
	if processingLag > a.telemetry.GeneratorProcessingLagMs {
		a.telemetry.GeneratorProcessingLagMs = processingLag
	}
	a.telemetry.TransportLagQuality = combineClockQuality(a.telemetry.TransportLagQuality, telemetry.TransportLagQuality, telemetry.TransportLagMs)
	if telemetry.TransportLagMs > a.telemetry.TransportLagMs {
		a.telemetry.TransportLagMs = telemetry.TransportLagMs
	}
}

func (a *distributedTelemetryAccumulator) result() *generatorrpc.MetricsTelemetry {
	if a.telemetry != nil {
		a.telemetry.DroppedResultsUnavailable = a.dropCounterUnavailable
	}
	return a.telemetry
}

// applyDistributedTelemetryTiming replaces collection timing only; source
// coverage, counters, and other live projections keep their snapshot ownership.
func applyDistributedTelemetryTiming(live *LiveMetrics, batch generatorrpc.MetricsBatch) {
	t := live.Telemetry
	t.CollectedAt, t.GeneratorPublishedAt, t.ControllerReceivedAt = time.Time{}, time.Time{}, time.Time{}
	t.GeneratorProcessingLagMs, t.TransportLagMs, t.SampleAgeMs = 0, 0, 0
	t.TransportLagQuality, t.SampleAgeQuality, t.collectedAtQuality = nil, nil, nil
	t.collectedAtOffsetMs, t.collectedAtBoundUnixMs = 0, 0
	t.qualifiedAt, t.qualifiedMonotonic = time.Time{}, 0
	t.newestCollectedAt = time.Time{}
	t.SampleAgeUnavailable = false
	if batch.Telemetry != nil {
		if batch.Telemetry.CollectedAtUnixMs > 0 {
			live.Telemetry.CollectedAt = time.UnixMilli(batch.Telemetry.CollectedAtUnixMs)
		}
		if batch.Telemetry.NewestCollectedAtUnixMs > 0 {
			live.Telemetry.newestCollectedAt = time.UnixMilli(batch.Telemetry.NewestCollectedAtUnixMs)
		}
		if batch.Telemetry.PublishedAtUnixMs > 0 {
			live.Telemetry.GeneratorPublishedAt = time.UnixMilli(batch.Telemetry.PublishedAtUnixMs)
		}
		if batch.Telemetry.ControllerReceivedAtUnixMs > 0 {
			live.Telemetry.ControllerReceivedAt = time.UnixMilli(batch.Telemetry.ControllerReceivedAtUnixMs)
		}
		if batch.Telemetry.GeneratorProcessingLagMs > 0 {
			live.Telemetry.GeneratorProcessingLagMs = batch.Telemetry.GeneratorProcessingLagMs
		} else if batch.GeneratorID != distributedAggregateGeneratorID && !live.Telemetry.CollectedAt.IsZero() && !live.Telemetry.GeneratorPublishedAt.IsZero() {
			// A fleet aggregate's timestamps come from different generators'
			// clocks; it carries each generator's own lag instead.
			live.Telemetry.GeneratorProcessingLagMs = float64(live.Telemetry.GeneratorPublishedAt.Sub(live.Telemetry.CollectedAt).Microseconds()) / 1000.0
		}
		live.Telemetry.TransportLagQuality = batch.Telemetry.TransportLagQuality
		live.Telemetry.collectedAtQuality, live.Telemetry.collectedAtOffsetMs = batch.Telemetry.CollectedAtQuality, batch.Telemetry.CollectedAtOffsetMs
		live.Telemetry.collectedAtBoundUnixMs = batch.Telemetry.CollectedAtBoundUnixMs
		live.Telemetry.qualifiedAt, live.Telemetry.qualifiedMonotonic = batch.Telemetry.ReceivedAt, batch.Telemetry.ReceivedMonotonic
		if batch.Telemetry.TransportLagMs > 0 || batch.Telemetry.TransportLagQuality != nil {
			// A qualified lag is already corrected; an implausible one stays
			// unavailable rather than recomputed from the raw clocks.
			live.Telemetry.TransportLagMs = batch.Telemetry.TransportLagMs
		} else if !live.Telemetry.GeneratorPublishedAt.IsZero() && !live.Telemetry.ControllerReceivedAt.IsZero() {
			live.Telemetry.TransportLagMs = float64(live.Telemetry.ControllerReceivedAt.Sub(live.Telemetry.GeneratorPublishedAt).Microseconds()) / 1000.0
		}
		live.Telemetry.SampleAgeMs = batch.Telemetry.SampleAgeMs
	}
	if live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.CollectedAt = live.Timestamp
		if batch.TimestampUnixMs != 0 {
			// No generator reported a sample (every generator stamps the
			// samples it publishes): the batch time is a generator's clock,
			// and there is no sample whose age a budget could judge.
			live.Telemetry.collectedAtQuality = &generatorrpc.ClockQuality{Reason: clockReasonUncalibrated}
			live.Telemetry.collectedAtOffsetMs, live.Telemetry.collectedAtBoundUnixMs = 0, 0
			live.Telemetry.SampleAgeUnavailable = true
		}
	}
}

// distributedPublicationTiming retains only the accepted timing facts needed
// to refresh collection ownership after waiting for the publication gate.
type distributedPublicationTiming struct {
	lifecycleState string
	telemetry      generatorrpc.MetricsTelemetry
}

// refreshDistributedPublicationTiming runs with the per-test publication gate
// held, before publishLiveMetrics computes age and diagnostics. A prepared
// revision may have waited behind a winner that published one of its final
// collections. Recheck exact proof against this revision's immutable snapshot,
// never against newer accepted metric payloads. No other publication for this
// run can install proof between this refresh and its guarded installation.
func (tr *TestRunner) refreshDistributedPublicationTiming(test *ManagedTest, live *LiveMetrics, publication distributedMetricsPublication) {
	if live == nil || live.Telemetry == nil || len(publication.timings) == 0 {
		return
	}
	tr.mu.RLock()
	changed := false
	for target, timing := range publication.timings {
		collection, identified := publication.collections[target]
		if identified && generatorrpc.IsMetricsCollectionFinal(timing.lifecycleState) &&
			timing.telemetry.CollectionPublished != test.runTotals.publishedWindows[target].sameCollection(collection) {
			changed = true
			break
		}
	}
	if !changed {
		tr.mu.RUnlock()
		return
	}
	timings := make(map[string]distributedPublicationTiming, len(publication.timings))
	targets := make([]string, 0, len(publication.timings))
	for target, timing := range publication.timings {
		collection, identified := publication.collections[target]
		timing.telemetry.CollectionPublished = identified && test.runTotals.publishedWindows[target].sameCollection(collection)
		timings[target] = timing
		targets = append(targets, target)
	}
	tr.mu.RUnlock()
	sort.Strings(targets)
	accumulator := distributedTelemetryAccumulator{publishedAt: time.Now()}
	for _, target := range targets {
		timing := timings[target]
		accumulator.add(timing.lifecycleState, &timing.telemetry)
	}
	applyDistributedTelemetryTiming(live, generatorrpc.MetricsBatch{
		GeneratorID:     distributedAggregateGeneratorID,
		TimestampUnixMs: live.Timestamp.UnixMilli(),
		Telemetry:       accumulator.result(),
	})
}

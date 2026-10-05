package api

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
)

// Evidence gaps an archive can record for the validity verdict.
const validityGapGeneratorArchiveMissing = "generator_archive_missing"

// archiveValidityIntervals are the archived intervals the validity verdict
// reads, one per interval summary with a known length.
func archiveValidityIntervals(manifest *RunArchiveManifest, summaries []IntervalSummaryRecord) []rca.ValidityInterval {
	started := archiveRunStarted(manifest)
	intervals := make([]rca.ValidityInterval, 0, len(summaries))
	for i, summary := range summaries {
		seconds, _ := loadComparisonIntervalSeconds(summaries, i, started)
		if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			continue
		}
		interval := rca.ValidityInterval{
			Start: summary.Timestamp.Add(-time.Duration(seconds * float64(time.Second))), End: summary.Timestamp,
			Attempts: summary.TotalTx + summary.TotalErrors, TPS: summary.TPS, AvgLatencyMs: summary.AvgResponseMs,
			TargetMissed: summary.ThroughputTargetMissed,
		}
		if telemetry := summary.Telemetry; telemetry != nil {
			if telemetry.DroppedResultsAvailable {
				dropped := telemetry.DroppedResults
				interval.DroppedResults = &dropped
			}
			shed := telemetry.SheddedArrivals
			interval.ShedArrivals = &shed
		}
		intervals = append(intervals, interval)
	}
	sort.SliceStable(intervals, func(i, j int) bool { return intervals[i].End.Before(intervals[j].End) })
	return intervals
}

func archiveRunStarted(manifest *RunArchiveManifest) time.Time {
	if manifest == nil || manifest.Provenance.StartedAt == nil {
		return time.Time{}
	}
	return *manifest.Provenance.StartedAt
}

// validityResultCounter counts archived result rows per interval. Dropped
// results are counted per protocol result, so their share needs every
// result, not only the business transactions an interval summary counts.
type validityResultCounter struct {
	intervals []rca.ValidityInterval
	counts    []int64
}

func newValidityResultCounter(intervals []rca.ValidityInterval) *validityResultCounter {
	return &validityResultCounter{intervals: intervals, counts: make([]int64, len(intervals))}
}

// add counts a result that started at at in the interval (start, end] that
// holds it.
func (c *validityResultCounter) add(at time.Time) {
	i := sort.Search(len(c.intervals), func(i int) bool { return !c.intervals[i].End.Before(at) })
	if i < len(c.intervals) && c.intervals[i].Start.Before(at) {
		c.counts[i]++
	}
}

// counted returns the intervals with their result counts.
func (c *validityResultCounter) counted() []rca.ValidityInterval {
	out := append([]rca.ValidityInterval(nil), c.intervals...)
	for i := range out {
		out[i].Results = &c.counts[i]
	}
	return out
}

// archiveRunValidity judges whether a run's load generators and telemetry
// could carry the load it reports, from the archived intervals and resource
// samples.
func archiveRunValidity(manifest *RunArchiveManifest, intervals []rca.ValidityInterval, summaries []IntervalSummaryRecord, resources []ResourceSample) *rca.Validity {
	if manifest == nil {
		return nil
	}
	validity := rca.AssessValidity(archiveValidityInput(manifest, intervals, summaries, resources), rca.DefaultValidityRules)
	return &validity
}

// archiveValidityInput is the evidence the validity verdict reads from an
// archive.
func archiveValidityInput(manifest *RunArchiveManifest, intervals []rca.ValidityInterval, summaries []IntervalSummaryRecord, resources []ResourceSample) rca.ValidityInput {
	input := rca.ValidityInput{ArrivalModel: archiveArrivalModel(manifest.Provenance.Workload), Intervals: intervals}

	// A distributed run names its generators; its samples must say which one
	// took them, or they are judged as one pool. Generators that reported
	// under other names are judged too.
	targets := map[string]bool{}
	for _, shard := range manifest.Provenance.GeneratorShards {
		if target := strings.TrimSpace(shard.Target); target != "" {
			targets[target] = true
		}
	}
	if coverage := manifest.SourceCoverage; coverage != nil {
		for _, source := range coverage.Sources {
			if target := strings.TrimSpace(source.Target); target != "" {
				targets[target] = true
				if !source.Collected {
					input.EvidenceGaps = appendUniqueStrings(input.EvidenceGaps, validityGapGeneratorArchiveMissing)
				}
			}
		}
	}
	distributed := len(targets) > 0
	input.GeneratorIdentityRecorded = true
	for _, sample := range resources {
		if externalOnlyResourceSample(sample) {
			continue
		}
		if distributed && sample.Generator == "" {
			input.GeneratorIdentityRecorded = false
		}
		input.Samples = append(input.Samples, rca.GeneratorSample{
			Generator: sample.Generator, At: sample.Timestamp,
			// The more saturated of process CPU against allocated cores and the
			// container's CPU against its limit.
			CPUPct: math.Max(sample.CPUProcessPct, sample.ContainerCPUUsagePct), StealPct: sample.CPUStealPct,
			ThrottledPeriods: sample.ContainerCPUThrottledPeriodsDelta, Periods: sample.ContainerCPUPeriodsDelta,
			OOMKills: sample.ContainerOOMKillCountDelta,
		})
	}
	if distributed && input.GeneratorIdentityRecorded {
		for target := range targets {
			input.Generators = append(input.Generators, target)
		}
		sort.Strings(input.Generators)
	}
	input.SteadyLevels = archiveSteadyLevels(summaries, archiveRunStarted(manifest), input.ArrivalModel)
	return input
}

// archiveSteadyLevels are the spans where the offered load held steady: the
// VUser count for a closed model, the target arrival rate for an open one
// (whose VUsers float with latency), and both for a mixed run.
func archiveSteadyLevels(summaries []IntervalSummaryRecord, started time.Time, arrivalModel string) []rca.TimeRange {
	levels := archiveLoadLevels(summaries, started, arrivalModel)
	if levels == nil {
		return nil
	}
	out := make([]rca.TimeRange, 0, len(levels))
	for _, level := range levels {
		out = append(out, rca.TimeRange{Start: level.Start, End: level.End})
	}
	return out
}

// archiveArrivalModel is the run's arrival model from its frozen workload:
// closed, open, mixed, or unknown when unrecorded.
func archiveArrivalModel(workload *rca.WorkloadKey) string {
	if workload == nil || !workload.Recorded() {
		return rca.ArrivalUnknown
	}
	models := map[string]bool{}
	for _, group := range workload.Groups {
		models[group.ArrivalModel] = true
	}
	switch {
	case len(models) == 1 && models[rca.ArrivalClosed]:
		return rca.ArrivalClosed
	case len(models) == 1 && models[rca.ArrivalOpen]:
		return rca.ArrivalOpen
	case len(models) == 2 && models[rca.ArrivalClosed] && models[rca.ArrivalOpen]:
		return rca.ArrivalMixed
	}
	return rca.ArrivalUnknown
}

// externalOnlyResourceSample is a row that carries only imported provider
// metrics, such as an ELK or Dynatrace capture total, not a generator sample.
func externalOnlyResourceSample(sample ResourceSample) bool {
	return len(sample.External) > 0 && sample.CPUProcessPct == 0 && sample.CPUFreePct == 0 && sample.MemProcessMB == 0 && sample.MemFreeMB == 0 && sample.ContainerCPUUsagePct == 0
}

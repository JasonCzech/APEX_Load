package api

import (
	"sort"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

// RunArchiveLiveTableSummary deliberately contains no rows, receipts, lease
// tokens, seed envelope, or recovery keys. Counts describe the committed table
// at this provenance capture; finalization records the separate pool outcome.
type RunArchiveLiveTableSummary struct {
	Observations *livetable.Observations `json:"observations,omitempty"`
	TableID      string                  `json:"table_id"`
	Alias        string                  `json:"alias"`
	Profile      string                  `json:"profile"`
	Durability   string                  `json:"durability"`
	EndMode      string                  `json:"end_mode"`
	State        string                  `json:"state"`
	Revision     uint64                  `json:"revision"`
	DataBytes    int64                   `json:"data_bytes"`
	Counts       livetable.Counts        `json:"counts"`
	// Operation telemetry covers this owner's serving interval. It is separate
	// from target request metrics and restarts after controller adoption.
	OpCount          uint64                               `json:"op_count"`
	OpErrors         uint64                               `json:"op_errors"`
	OpUnknown        uint64                               `json:"op_unknown"`
	OpLatencyTotalNS uint64                               `json:"op_latency_total_ns"`
	OpLatencyMaxNS   uint64                               `json:"op_latency_max_ns"`
	OpLatencyBuckets [8]uint64                            `json:"op_latency_buckets"`
	Finalization     *storage.LiveTableFinalizationResult `json:"finalization,omitempty"`
}

func cloneLiveTableArchiveSummaries(in []RunArchiveLiveTableSummary) []RunArchiveLiveTableSummary {
	if in == nil {
		return nil
	}
	out := append([]RunArchiveLiveTableSummary{}, in...)
	for i := range out {
		if out[i].Observations != nil {
			out[i].Observations = boundedLiveTableObservations(out[i].Observations)
		}
		if out[i].Finalization != nil {
			copy := *out[i].Finalization
			out[i].Finalization = &copy
		}
	}
	return out
}

// Call only outside tr.mu, as for the other provenance enrichment helpers.
func (tr *TestRunner) liveTableArchiveSummaries(testID string) []RunArchiveLiveTableSummary {
	run := tr.liveRun(testID)
	if run == nil {
		return nil
	}
	run.mu.RLock()
	defer run.mu.RUnlock()
	out := make([]RunArchiveLiveTableSummary, 0, len(run.tables))
	for _, rt := range run.tables {
		stats := rt.summary
		if rt.table != nil {
			stats = rt.table.Stats()
		}
		state := "active"
		if stats.Unavailable {
			state = "unavailable"
		} else if stats.Closed || run.closed {
			state = "closed"
		}
		item := RunArchiveLiveTableSummary{TableID: rt.record.ID, Alias: rt.binding.Alias, Profile: rt.binding.LiveOptions.Profile, Durability: rt.binding.LiveOptions.Durability, EndMode: rt.binding.LiveOptions.EndMode, State: state, Revision: stats.Revision, DataBytes: stats.DataBytes, Counts: stats.Counts}
		item.OpCount, item.OpErrors, item.OpUnknown = stats.OpCount, stats.OpErrors, stats.OpUnknown
		item.OpLatencyTotalNS, item.OpLatencyMaxNS, item.OpLatencyBuckets = stats.OpLatencyTotalNS, stats.OpLatencyMaxNS, stats.OpLatencyBuckets
		if stats.Observations.Version != 0 {
			observed := stats.Observations
			item.Observations = &observed
		}
		if rt.final != nil {
			result := *rt.final
			item.Finalization = &result
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

// Imported archives do not necessarily originate from the table core. Apply
// the same bounded, value-free event contract before rendering their evidence.
func boundedLiveTableObservations(in *livetable.Observations) *livetable.Observations {
	if in == nil {
		return nil
	}
	out := *in
	out.Events = nil
	if in.Version != 1 {
		out.Incomplete = true
	}
	drop := func(count uint64) {
		out.Incomplete = true
		if ^uint64(0)-out.DroppedEvents < count {
			out.DroppedEvents = ^uint64(0)
		} else {
			out.DroppedEvents += count
		}
	}
	limit := min(len(in.Events), 16)
	if len(in.Events) > limit {
		drop(uint64(len(in.Events) - limit))
	}
	for _, event := range in.Events[:limit] {
		valid := event.Count > 0 && !event.First.IsZero() && !event.Last.Before(event.First)
		switch event.Kind {
		case "empty", "capacity_rejection", "unknown_outcome", "unknown_pop_attempt", "recovery_observation_gap":
		default:
			valid = false
		}
		if !valid {
			drop(1)
			continue
		}
		out.Events = append(out.Events, event)
	}
	return &out
}

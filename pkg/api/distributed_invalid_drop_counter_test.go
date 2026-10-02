package api

import (
	"math"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

func TestRetainedDropCounterCheckedAddition(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		current, incoming, want int64
		valid                   bool
	}{
		{name: "measured zero", valid: true},
		{name: "positive addition", current: 1, incoming: 2, want: 3, valid: true},
		{name: "negative incoming", current: 1, incoming: -1, want: 1},
		{name: "negative current", current: -1, incoming: 1, want: 1},
		{name: "both invalid", current: math.MinInt64, incoming: -1},
		{name: "exact maximum", current: math.MaxInt64 - 1, incoming: 1, want: math.MaxInt64, valid: true},
		{name: "overflow", current: math.MaxInt64, incoming: 1, want: math.MaxInt64},
		{name: "large overflow", current: math.MaxInt64, incoming: math.MaxInt64, want: math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := mergeRetainedDropCounters(tc.current, tc.incoming)
			if got != tc.want || valid != tc.valid {
				t.Fatalf("sum=%d valid=%v; want %d/%v", got, valid, tc.want, tc.valid)
			}
		})
	}
}

func TestDistributedInvalidDropCountersCannotCancelObservedLoss(t *testing.T) {
	for _, tc := range []struct {
		name      string
		counters  []int64
		want      int64
		available bool
	}{
		{name: "all measured zero", counters: []int64{0, 0}, available: true},
		{name: "valid positive sum", counters: []int64{1, 2}, want: 3, available: true},
		{name: "invalid second peer", counters: []int64{1, -1}, want: 1},
		{name: "invalid first peer", counters: []int64{-1, 1}, want: 1},
		{name: "invalid peer with zero", counters: []int64{0, -1}},
		{name: "all invalid", counters: []int64{-1, math.MinInt64}},
		{name: "exact maximum", counters: []int64{math.MaxInt64 - 1, 1}, want: math.MaxInt64, available: true},
		{name: "overflow must retain loss", counters: []int64{math.MaxInt64, 1}, want: math.MaxInt64},
		{name: "wrap to zero prevented", counters: []int64{math.MaxInt64, math.MaxInt64, 2}, want: math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			batches := make(map[string]generatorrpc.MetricsBatch)
			originals := make(map[string]generatorrpc.MetricsTelemetry)
			archiveSources := make([]*TelemetryHealth, 0, len(tc.counters))
			archiveOriginals := make([]TelemetryHealth, 0, len(tc.counters))
			for i, counter := range tc.counters {
				key := string(rune('a' + i))
				telemetry := generatorrpc.MetricsTelemetry{DroppedResults: counter}
				batches[key] = generatorrpc.MetricsBatch{Telemetry: &telemetry}
				originals[key] = telemetry
				source := TelemetryHealth{DroppedResultsAvailable: true, DroppedResults: counter}
				archiveSources = append(archiveSources, &source)
				archiveOriginals = append(archiveOriginals, source)
			}
			batch, _, _ := aggregateDistributedMetricsBatches("run", batches)
			live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, batch, 0, nil)
			if live.Telemetry == nil || live.Telemetry.DroppedResults != tc.want || live.Telemetry.DroppedResultsAvailable != tc.available {
				t.Fatalf("live=%+v; want %d/%v", live.Telemetry, tc.want, tc.available)
			}
			for key, original := range originals {
				if !reflect.DeepEqual(*batches[key].Telemetry, original) {
					t.Fatalf("source %s mutated", key)
				}
			}
			var archived *TelemetryHealth
			for _, source := range archiveSources {
				archived = mergeRemoteArchiveSummaryTelemetry(archived, source)
			}
			if archived == nil || archived.DroppedResults != tc.want || archived.DroppedResultsAvailable != tc.available {
				t.Fatalf("archive=%+v; want %d/%v", archived, tc.want, tc.available)
			}
			for i, original := range archiveOriginals {
				if !reflect.DeepEqual(*archiveSources[i], original) {
					t.Fatalf("archive source %d mutated", i)
				}
			}
			for _, telemetry := range []*TelemetryHealth{live.Telemetry, archived} {
				evidence := baselineTelemetryEvidence("run", []IntervalSummaryRecord{{Telemetry: telemetry}})
				wantEvidence := tc.available || tc.want > 0
				if (evidence.DroppedResults != nil) != wantEvidence {
					t.Fatalf("malformed cohort invented zero evidence: %+v", evidence)
				}
				if evidence.DroppedResults != nil && *evidence.DroppedResults != tc.want {
					t.Fatalf("known positive loss disappeared: %+v", evidence)
				}
			}
		})
	}
}

func TestDistributedNegativeDropCounterPresenceIsUnavailable(t *testing.T) {
	telemetry := generatorrpc.MetricsTelemetry{DroppedResults: -1}
	before := telemetry
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, generatorrpc.MetricsBatch{Telemetry: &telemetry}, 0, nil)
	if live.Telemetry == nil || live.Telemetry.DroppedResultsAvailable {
		t.Fatalf("negative counter qualified as measured: %+v", live.Telemetry)
	}
	if !reflect.DeepEqual(telemetry, before) {
		t.Fatal("input telemetry mutated")
	}
}

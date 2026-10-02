package api

import (
	"github.com/jasonczech/apexload/pkg/storage"
	"testing"
	"time"
)

func TestRuntimeHistoryRestoresDropCounterAvailability(t *testing.T) {
	for _, tc := range []struct {
		name      string
		record    storage.IntervalSummaryRecord
		available bool
		wantNil   bool
	}{
		{name: "measured zero", record: storage.IntervalSummaryRecord{DroppedResultsAvailable: true}, available: true},
		{name: "legacy metadata", record: storage.IntervalSummaryRecord{CollectedAt: time.Now()}},
		{name: "legacy positive", record: storage.IntervalSummaryRecord{DroppedResults: 2}},
		{name: "absent", wantNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := intervalSummaryRecordToLiveMetrics(tc.record)
			if (live.Telemetry == nil) != tc.wantNil {
				t.Fatalf("telemetry presence %+v", live.Telemetry)
			}
			if live.Telemetry != nil && (live.Telemetry.DroppedResultsAvailable != tc.available || live.Telemetry.DroppedResults != tc.record.DroppedResults) {
				t.Fatalf("availability or counter lost: %+v", live.Telemetry)
			}
		})
	}
}

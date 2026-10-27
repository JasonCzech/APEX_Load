package importreview

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

func TestNewRecordingBaselineNilForEmpty(t *testing.T) {
	if got := NewRecordingBaseline(nil); got != nil {
		t.Fatalf("NewRecordingBaseline(nil) = %#v, want nil", got)
	}
	if got := NewRecordingBaseline([]correlate.Observation{}); got != nil {
		t.Fatalf("NewRecordingBaseline(empty) = %#v, want nil", got)
	}
}

func TestBoundRecordingBaselineObservationsCapsCount(t *testing.T) {
	observations := make([]correlate.Observation, MaxRecordedBaselineObservations+7)
	for index := range observations {
		observations[index] = correlate.Observation{ID: index, ResponseBody: "ok"}
	}
	baseline := NewRecordingBaseline(observations)
	if baseline == nil {
		t.Fatal("NewRecordingBaseline returned nil for a non-empty baseline")
	}
	if len(baseline.Observations) != MaxRecordedBaselineObservations {
		t.Fatalf("retained %d observations, want %d", len(baseline.Observations), MaxRecordedBaselineObservations)
	}
	if baseline.RequestCount != len(observations) {
		t.Fatalf("RequestCount = %d, want original %d", baseline.RequestCount, len(observations))
	}
	if baseline.Observations[len(baseline.Observations)-1].ID != MaxRecordedBaselineObservations-1 {
		t.Fatalf("bound did not keep the leading prefix: last ID %d", baseline.Observations[len(baseline.Observations)-1].ID)
	}
}

func TestBoundRecordingBaselineObservationsCapsEstimatedBytes(t *testing.T) {
	// Each body fits on its own, but two do not fit together under the
	// six-bytes-per-input-byte estimate.
	body := strings.Repeat("x", MaxRecordedBaselinePlaintextSize/6/2+1024)
	observations := []correlate.Observation{
		{ID: 0, ResponseBody: body},
		{ID: 1, ResponseBody: body},
		{ID: 2, ResponseBody: "small"},
	}
	bounded := BoundRecordingBaselineObservations(observations)
	if len(bounded) != 1 || bounded[0].ID != 0 {
		t.Fatalf("bounded = %d observations, want only the first", len(bounded))
	}
}

func TestBoundRecordingBaselineObservationsFullyTruncatedKeepsCount(t *testing.T) {
	huge := strings.Repeat("y", MaxRecordedBaselinePlaintextSize/6+1)
	baseline := NewRecordingBaseline([]correlate.Observation{{ID: 0, ResponseBody: huge}, {ID: 1}})
	if baseline == nil {
		t.Fatal("a fully truncated baseline must still witness its request count")
	}
	if len(baseline.Observations) != 0 || baseline.RequestCount != 2 {
		t.Fatalf("baseline = %d observations / count %d, want 0 / 2", len(baseline.Observations), baseline.RequestCount)
	}
}

func TestRecordingBaselineCloneIsIndependent(t *testing.T) {
	original := NewRecordingBaseline([]correlate.Observation{{ID: 0}, {ID: 1}})
	clone := original.Clone()
	clone.Observations[0].ID = 99
	clone.RequestCount = 7
	if original.Observations[0].ID != 0 || original.RequestCount != 2 {
		t.Fatalf("Clone shares state with the original: %#v", original)
	}
	var nilBaseline *RecordingBaseline
	if nilBaseline.Clone() != nil {
		t.Fatal("nil baseline must clone to nil")
	}
}

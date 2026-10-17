package storage

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestNativeIntervalMetricKeepsItsSketch(t *testing.T) {
	sketch := engine.NewDurationSketch()
	sketch.Add(1e6)
	sketch.Add(3e7)
	snapshot := sketch.Snapshot()
	metric := func() NativeIntervalMetric {
		return NativeIntervalMetric{Count: 2, DurationSketch: engine.CloneDurationSketchSnapshot(&snapshot)}
	}
	other := metric()
	evidence := &NativeIntervalEvidence{Version: 2, WindowSeconds: 1,
		Dimensions: map[string]map[string]NativeIntervalMetric{"transaction": {"Checkout": metric()}},
		Joint:      &NativeJointEvidence{Population: "action", Availability: "measured", Other: &other}}

	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var decoded NativeIntervalEvidence
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.Dimensions["transaction"]["Checkout"].DurationSketch; !reflect.DeepEqual(got, &snapshot) {
		t.Fatalf("stored sketch = %+v, want %+v", got, snapshot)
	}

	clone := CloneNativeIntervalEvidence(evidence)
	clone.Dimensions["transaction"]["Checkout"].DurationSketch.Bins[0].Count++
	clone.Joint.Other.DurationSketch.Bins[0].Count++
	if evidence.Dimensions["transaction"]["Checkout"].DurationSketch.Bins[0].Count != snapshot.Bins[0].Count ||
		evidence.Joint.Other.DurationSketch.Bins[0].Count != snapshot.Bins[0].Count {
		t.Fatal("clone shares sketch buckets with the original")
	}
}

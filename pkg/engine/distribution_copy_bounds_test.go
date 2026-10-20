package engine

import "testing"

func TestDurationSketchCopyRejectsOversizedBeforeRetention(t *testing.T) {
	for _, n := range []int{0, 1, DurationSketchMaxBuckets, DurationSketchMaxBuckets + 1, DurationSketchMaxBuckets * 2} {
		source := &DurationSketchSnapshot{Version: DurationSketchSnapshotVersion, Count: 1, Bins: make([]DurationSketchBin, n)}
		if n > 0 {
			source.Bins[0] = DurationSketchBin{Index: 1, Count: 1}
		}
		copy := CloneDurationSketchSnapshot(source)
		if n > DurationSketchMaxBuckets {
			if copy != nil {
				t.Fatalf("retained oversized sketch of %d bins", n)
			}
		} else {
			if copy == nil || len(copy.Bins) != n {
				t.Fatalf("boundary %d lost", n)
			}
			if n > 0 {
				copy.Bins[0].Count = 2
				if source.Bins[0].Count != 1 {
					t.Fatal("copy aliases original")
				}
			}
		}
		if len(source.Bins) != n {
			t.Fatal("source was truncated")
		}
	}
	if CloneDurationSketchSnapshot(nil) != nil {
		t.Fatal("absent became a sketch")
	}
}

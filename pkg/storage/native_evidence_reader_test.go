package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestForEachNativeIntervalEvidencePagesInTimestampOrder(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveTest(&TestRecord{ID: "run", Name: "run", ProjectID: DefaultProjectID, Status: "COMPLETE", Groups: []any{}, StartTime: time.Now()}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	// Rows span several pages; every third row has no native evidence. Rows are
	// written out of order to prove the reader sorts by timestamp.
	const rows = 3*nativeEvidencePageSize + 5
	for i := rows - 1; i >= 0; i-- {
		at := start.Add(time.Duration(i) * 5 * time.Second)
		metrics := map[string]any{"timestamp": at}
		if i%3 != 0 {
			metrics["native_evidence"] = &NativeIntervalEvidence{Version: 2, WindowSeconds: 5, Dimensions: map[string]map[string]NativeIntervalMetric{"transaction": {"Purchase": {Count: int64(i)}}}}
		}
		if err := store.SaveHistory("run", at, metrics); err != nil {
			t.Fatal(err)
		}
	}
	var seen []int64
	var previous time.Time
	err = store.ForEachNativeIntervalEvidence(context.Background(), "run", func(at time.Time, evidence *NativeIntervalEvidence) error {
		if !previous.IsZero() && !at.After(previous) {
			t.Fatalf("timestamps out of order: %s after %s", at, previous)
		}
		previous = at
		seen = append(seen, evidence.Dimensions["transaction"]["Purchase"].Count)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for i := 0; i < rows; i++ {
		if i%3 != 0 {
			if want >= len(seen) || seen[want] != int64(i) {
				t.Fatalf("row %d missing or out of place in %v", i, seen)
			}
			want++
		}
	}
	if want != len(seen) {
		t.Fatalf("read %d rows, want %d", len(seen), want)
	}
	stop := errors.New("stop")
	calls := 0
	if err := store.ForEachNativeIntervalEvidence(context.Background(), "run", func(time.Time, *NativeIntervalEvidence) error {
		calls++
		return stop
	}); !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("callback error not propagated: %v after %d calls", err, calls)
	}
}

func TestForEachNativeIntervalEvidenceReadsDuplicateTimestampsOnce(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "native.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SaveTest(&TestRecord{ID: "run", Name: "run", ProjectID: DefaultProjectID, Status: "COMPLETE", Groups: []any{}, StartTime: time.Now()}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 4, 12, 0, 0, 125_000_000, time.UTC)
	// Distinct sub-second rows, then a run of one repeated timestamp that
	// crosses a page boundary and fills a whole page, then distinct rows again.
	var stamps []time.Time
	for i := 0; i < nativeEvidencePageSize-3; i++ {
		stamps = append(stamps, start.Add(time.Duration(i)*1500*time.Millisecond))
	}
	repeated := start.Add(time.Hour)
	for i := 0; i < nativeEvidencePageSize+7; i++ {
		stamps = append(stamps, repeated)
	}
	for i := 1; i <= 3; i++ {
		stamps = append(stamps, repeated.Add(time.Duration(i)*time.Second))
	}
	for i, at := range stamps {
		evidence := &NativeIntervalEvidence{Version: 2, WindowSeconds: 5, Dimensions: map[string]map[string]NativeIntervalMetric{"transaction": {"Purchase": {Count: int64(i)}}}}
		if err := store.SaveHistory("run", at, map[string]any{"timestamp": at, "native_evidence": evidence}); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int64]int{}
	var previous time.Time
	if err := store.ForEachNativeIntervalEvidence(context.Background(), "run", func(at time.Time, evidence *NativeIntervalEvidence) error {
		if at.Before(previous) {
			t.Fatalf("timestamps out of order: %s after %s", at, previous)
		}
		previous = at
		seen[evidence.Dimensions["transaction"]["Purchase"].Count]++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != len(stamps) {
		t.Fatalf("read %d distinct rows, want %d", len(seen), len(stamps))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("row %d read %d times", id, n)
		}
	}
}

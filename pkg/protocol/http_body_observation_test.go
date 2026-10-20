package protocol

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestHTTPBodyObservationBoundsAndMissingness(t *testing.T) {
	var samples []HTTPBodyObservation
	observe := func(s HTTPBodyObservation) { samples = append(samples, s) }
	parent := WithHTTPBodyObserver(context.Background(), observe)
	for _, tc := range []struct {
		size        int64
		compression string
	}{{-1, "none"}, {0, "unknown"}, {0, "none"}, {12, "gzip"}, {4, "deflate"}} {
		ObserveHTTPBody(WithHTTPBodyObserver(context.Background(), observe), tc.size, tc.compression)
	}
	if !reflect.DeepEqual(samples, []HTTPBodyObservation{{0, "none"}, {12, "gzip"}, {4, "deflate"}}) {
		t.Fatal(samples)
	}
	ObserveHTTPBody(context.Background(), 7, "none")
	ObserveHTTPBody(WithHTTPBodyObserver(parent, nil), 7, "none")
	if len(samples) != 3 {
		t.Fatal("absent observer inherited or fabricated sample", samples)
	}
}

func TestHTTPBodyObservationRetainsOnlyInitialPreparation(t *testing.T) {
	var count atomic.Int64
	ctx := WithHTTPBodyObserver(context.Background(), func(s HTTPBodyObservation) {
		if s.Bytes < 1 || s.Bytes > 50 {
			t.Error(s)
		}
		count.Add(1)
	})
	var workers sync.WaitGroup
	for i := int64(1); i <= 50; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); ObserveHTTPBody(ctx, i, "none") }()
	}
	workers.Wait()
	if count.Load() != 1 {
		t.Fatal("nested/concurrent preparations replaced the initial body", count.Load())
	}
	ObserveHTTPBody(nil, 1, "none")
}

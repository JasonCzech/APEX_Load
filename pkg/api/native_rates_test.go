package api

import (
	"context"
	"github.com/jasonczech/apexload/pkg/storage"
	"testing"
	"time"
)

func TestNativeRatesAcrossArchiveAndLive(t *testing.T) {
	record, start := nativeSeriesFixture(t)
	for _, tc := range []struct {
		metric string
		want   float64
	}{{"observation_rate", .3}, {"error_observation_rate", .1}, {"success_observation_rate", .2}} {
		q := nativeEvidenceQuery("archive")
		q.Metric = tc.metric
		q.Aggregation = "rate"
		out, err := buildNativeGroupedArchive(context.Background(), record, q)
		if err != nil || *out.Series[0].Points[0].Value != tc.want || out.Series[0].Points[1].Value != nil || out.Series[0].Unit != "observations/s" {
			t.Fatalf("grouped %+v %v", out, err)
		}
		q.EvidenceMode = ""
		q.Dimensions = nil
		q.MaxSeries = 1
		single, err := buildNativeArchiveSeries(context.Background(), record, q)
		if err != nil || *single.Buckets[0].Value != tc.want || single.Buckets[1].Value != nil {
			t.Fatalf("archive %+v %v", single, err)
		}
	}
	m := nativeBucketMetric(1, 2, 3)
	m.Errors = 1
	h := &LiveMetrics{Timestamp: start.Add(2 * time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Version: 2, WindowSeconds: 2, Dimensions: map[string]map[string]storage.NativeIntervalMetric{"action": {"checkout": m}}}}
	q := nativeEvidenceQuery("live")
	q.Metric = "error_observation_rate"
	q.Aggregation = "rate"
	q.BucketSeconds = 2
	q.EndSeconds = 3
	out, err := buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h}, q)
	if err != nil || *out.Series[0].Points[0].Value != .5 {
		t.Fatalf("source %+v %v", out, err)
	}
	q.LiveBucketMode = "merged"
	out, err = buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h}, q)
	if err != nil || *out.Series[0].Points[0].Value != .5 || out.Series[0].Points[1].Value != nil {
		t.Fatalf("merged %+v %v", out, err)
	}
	h.NativeEvidence.WindowSeconds = 1
	out, err = buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h}, q)
	if err != nil || out.Series[0].Points[0].Value != nil {
		t.Fatalf("partial coverage scored %+v %v", out, err)
	}
	// A clipped final bucket uses its actual one-second span, not configured width.
	h.Timestamp = start.Add(3 * time.Second)
	out, err = buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h}, q)
	if err != nil || *out.Series[0].Points[1].Value != 1 {
		t.Fatalf("clipped %+v %v", out, err)
	}
}

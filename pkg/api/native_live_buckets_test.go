package api

import (
	"context"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
	"testing"
	"time"
)

func nativeBucketMetric(values ...float64) storage.NativeIntervalMetric {
	d := engine.NewTDigest(200)
	sum := 0.0
	for _, v := range values {
		d.Add(v * float64(time.Millisecond))
		sum += v
	}
	snapshot := d.Snapshot()
	return storage.NativeIntervalMetric{Count: int64(len(values)), Avg: sum / float64(len(values)), DurationDigest: &snapshot, P95: 99999}
}
func TestNativeMergedLiveBucketsUseDistributionAndJoint(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	small := make([]float64, 99)
	for i := range small {
		small[i] = 1
	}
	histories := []*LiveMetrics{}
	for i, m := range []storage.NativeIntervalMetric{nativeBucketMetric(small...), nativeBucketMetric(1000)} {
		histories = append(histories, &LiveMetrics{Timestamp: start.Add(time.Duration(i+1) * time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Version: 2, WindowSeconds: 1, Dimensions: map[string]map[string]storage.NativeIntervalMetric{"action": {"checkout": m}, "action_property": {engine.ActionPropertyKey("checkout", "kind", "paid"): m, engine.ActionPropertyKey("other", "kind", "paid"): nativeBucketMetric(9000)}}}})
	}
	q := nativeEvidenceQuery("live")
	q.LiveBucketMode = "merged"
	q.BucketSeconds = 2
	q.EndSeconds = 4
	q.Filters = map[string]string{"action": "checkout"}
	q.PropertyName = "kind"
	q.PropertyValue = "paid"
	if err := storage.ValidateNativeEvidenceQuery(q); err != nil {
		t.Fatal(err)
	}
	out, err := buildNativeLiveEvidence(context.Background(), start, histories, q)
	if err != nil {
		t.Fatal(err)
	}
	p := out.Series[0].Points[0]
	if out.LiveBucketMode != "merged" || p.Count != 100 || p.Value == nil || *p.Value > 10 || p.Coverage == nil || *p.Coverage != 1 || out.Series[0].Points[1].Value != nil {
		t.Fatalf("merged %+v", out)
	}
	q.Aggregation = "max"
	out, err = buildNativeLiveEvidence(context.Background(), start, histories, q)
	if err != nil || *out.Series[0].Points[0].Value != 1000 {
		t.Fatalf("max %+v %v", out, err)
	}
	histories[1].NativeEvidence.Incomplete = true
	out, err = buildNativeLiveEvidence(context.Background(), start, histories, q)
	if err != nil || out.Series[0].Points[0].Value != nil {
		t.Fatalf("partial evidence %+v %v", out, err)
	}
}
func TestNativeMergedLiveRejectsLegacyInvalidAndDuplicate(t *testing.T) {
	start := time.Now().UTC()
	q := nativeEvidenceQuery("live")
	q.LiveBucketMode = "merged"
	h := &LiveMetrics{Timestamp: start.Add(time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Version: 1, WindowSeconds: 1}}
	if _, err := buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h}, q); err == nil {
		t.Fatal("legacy evidence merged")
	}
	h.NativeEvidence.Version = 2
	h.NativeEvidence.Dimensions = map[string]map[string]storage.NativeIntervalMetric{"action": {"a": nativeBucketMetric(10)}}
	if _, err := buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h, h}, q); err == nil {
		t.Fatal("duplicate counted")
	}
	m := h.NativeEvidence.Dimensions["action"]["a"]
	m.DurationDigest.Count = 20
	h.NativeEvidence.Dimensions["action"]["a"] = m
	out, err := buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h}, q)
	if err != nil || out.Series[0].Points[0].Value != nil {
		t.Fatalf("invalid digest scored %+v %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildNativeLiveEvidence(ctx, start, []*LiveMetrics{h}, q); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestNativeMergedSourceFramesDeduplicateFleetRepublication(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	m := nativeBucketMetric(10, 20)
	batch := generatorrpc.MetricsBatch{ActionMetricsVersion: 2, BatchSequence: 1, ExecutionGeneration: 3, TimestampUnixMs: start.Add(time.Second).UnixMilli(), WindowMs: 1000, ActionMetrics: []generatorrpc.MetricAggregate{{Name: "checkout", Count: m.Count, AvgMs: m.Avg, DurationDigest: m.DurationDigest}}}
	peers := map[string]generatorrpc.MetricsBatch{"a": batch, "b": batch}
	a := nativePublicationEvidence("a", batch, peers)
	b := nativePublicationEvidence("b", batch, peers)
	histories := []*LiveMetrics{
		{Timestamp: start.Add(time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Publication: a}},
		{Timestamp: start.Add(1500 * time.Millisecond), NativeEvidence: &storage.NativeIntervalEvidence{Publication: b}},
		{Timestamp: start.Add(2 * time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Publication: a}},
	}
	q := nativeEvidenceQuery("live")
	q.LiveBucketMode = "merged"
	q.BucketSeconds = 2
	q.EndSeconds = 4
	q.Metric = "observation_count"
	q.Aggregation = "count"
	out, err := buildNativeLiveEvidence(context.Background(), start, histories, q)
	if err != nil || len(out.Series) != 1 || out.Series[0].Points[0].Count != 4 || *out.Series[0].Points[0].Value != 4 {
		t.Fatalf("resident batch counted again %+v %v", out, err)
	}
	out, err = buildNativeLiveEvidence(context.Background(), start, histories[:1], q)
	if err != nil || out.Series[0].Points[0].Value != nil || *out.Series[0].Points[0].Coverage != 0 {
		t.Fatalf("missing peer hidden %+v %v", out, err)
	}
	clone := storage.CloneNativeIntervalEvidence(histories[0].NativeEvidence)
	clone.Publication.Dimensions["action"]["checkout"].DurationDigest.Centroids[0].Mean = 1e12
	if a.Dimensions["action"]["checkout"].DurationDigest.Centroids[0].Mean == 1e12 {
		t.Fatal("source frame clone alias")
	}
}

func TestNativeMergedLiveDimensionIntersections(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	q := nativeEvidenceQuery("live")
	q.LiveBucketMode = "merged"
	q.Dimensions = []string{"phase"}
	q.Filters = map[string]string{"action": "checkout", "network_profile": "slow", "hierarchy": "cart"}
	h := &LiveMetrics{Timestamp: start.Add(time.Second), NativeEvidence: &storage.NativeIntervalEvidence{Version: 3, WindowSeconds: 1, Dimensions: map[string]map[string]storage.NativeIntervalMetric{"action_dimensions": {
		engine.ActionDimensionKey("checkout", "slow", "cart", "init"):    nativeBucketMetric(10),
		engine.ActionDimensionKey("checkout", "slow", "cart", "actions"): nativeBucketMetric(20),
		engine.ActionDimensionKey("checkout", "fast", "cart", "actions"): nativeBucketMetric(9000),
		engine.ActionDimensionKey("other", "slow", "cart", "actions"):    nativeBucketMetric(9000),
	}}}}
	if err := storage.ValidateNativeEvidenceQuery(q); err != nil {
		t.Fatal(err)
	}
	out, err := buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h}, q)
	if err != nil || len(out.Series) != 2 || out.Series[0].Name != "actions" || *out.Series[0].Points[0].Value != 20 || *out.Series[1].Points[0].Value != 10 {
		t.Fatalf("intersections %+v %v", out, err)
	}
	h.NativeEvidence.Version = 2
	if _, err = buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h}, q); err == nil {
		t.Fatal("legacy peer accepted")
	}
	h.NativeEvidence.Version = 3
	h.NativeEvidence.Dimensions["action_dimensions"][engine.ActionDimensionKey("checkout", "slow", "cart", "")] = nativeBucketMetric(1)
	if _, err = buildNativeLiveEvidence(context.Background(), start, []*LiveMetrics{h}, q); err == nil {
		t.Fatal("missing label scored")
	}
	q.PropertyName = "kind"
	q.PropertyValue = "paid"
	if storage.ValidateNativeEvidenceQuery(q) == nil {
		t.Fatal("unretained property intersection accepted")
	}
}
